package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gobs/args"
	"github.com/gobs/pretty"
	"github.com/gobs/simplejson"
	"github.com/raff/godet"
)

func runCommand(commandString string) error {
	parts := args.GetArgs(commandString)
	cmd := exec.Command(parts[0], parts[1:]...)
	return cmd.Start()
}

func limit(s string, l int) string {
	if len(s) > l {
		return s[:l] + "..."
	}
	return s
}

func documentNode(remote *godet.RemoteDebugger, verbose bool) int {
	res, err := remote.GetDocument()
	if err != nil {
		log.Fatal("error getting document: ", err)
	}

	if verbose {
		pretty.PrettyPrint(res)
	}

	doc := simplejson.AsJson(res)
	return doc.GetPath("root", "nodeId").MustInt(-1)
}

func main() {
	var chromeapp string

	switch runtime.GOOS {
	case "darwin":
		for _, c := range []string{
			"/Applications/Google Chrome Canary.app",
			"/Applications/Google Chrome.app",
		} {
			// MacOS apps are actually folders
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				chromeapp = fmt.Sprintf("open %q --args", c)
				break
			}
		}

	case "linux":
		for _, c := range []string{
			"headless_shell",
			"chromium",
			"google-chrome-beta",
			"google-chrome-unstable",
			"google-chrome-stable"} {
			if _, err := exec.LookPath(c); err == nil {
				chromeapp = c
				break
			}
		}

	case "windows":
	}

	if chromeapp != "" {
		if chromeapp == "headless_shell" {
			chromeapp += " --no-sandbox"
		} else {
			chromeapp += " --headless"
		}

		chromeapp += " --remote-debugging-port=9222 --disable-extensions --disable-gpu about:blank"
	}

	cmd := flag.String("cmd", chromeapp, "command to execute to start the browser")
	port := flag.String("port", "localhost:9222", "Chrome remote debugger port")
	verbose := flag.Bool("verbose", false, "verbose logging")
	version := flag.Bool("version", false, "display remote devtools version")
	listtabs := flag.Bool("tabs", false, "show list of open tabs")
	seltab := flag.Int("tab", 0, "select specified tab if available")
	newtab := flag.Bool("new", false, "always open a new tab")
	filter := flag.String("filter", "page", "filter tab list")
	domains := flag.Bool("domains", false, "show list of available domains")
	requests := flag.Bool("requests", false, "show request notifications")
	responses := flag.Bool("responses", false, "show response notifications")
	allEvents := flag.Bool("all-events", false, "enable all events")
	logev := flag.Bool("log", false, "show log/console messages")
	query := flag.String("query", "", "query against current document")
	eval := flag.String("eval", "", "evaluate expression")
	screenshot := flag.Bool("screenshot", false, "take a screenshot")
	pdf := flag.Bool("pdf", false, "save current page as PDF")
	control := flag.String("control", "", "control navigation (proceed,cancel,cancelIgnore)")
	block := flag.String("block", "", "block specified URLs or pattenrs. Use '|' as separator")
	html := flag.Bool("html", false, "get outer HTML for current page")
	setHtml := flag.String("set-html", "", "set outer HTML for current page")
	wait := flag.Bool("wait", false, "wait for more events")
	flag.Parse()

	if *cmd != "" {
		if err := runCommand(*cmd); err != nil {
			log.Println("cannot start browser", err)
		}
	}

	var remote *godet.RemoteDebugger
	var err error

	for i := 0; i < 10; i++ {
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}

		remote, err = godet.Connect(*port, *verbose)
		if err == nil {
			break
		}

		log.Println("connect", err)
	}

	if err != nil {
		log.Fatal("cannot connect to browser")
	}

	defer remote.Close()

	done := make(chan bool)
	should_wait := true

	v, err := remote.Version()
	if err != nil {
		log.Fatal("cannot get version: ", err)
	}

	if *version {
		pretty.PrettyPrint(v)
	} else {
		log.Println("connected to", v.Browser, "protocol version", v.ProtocolVersion)
	}

	if *listtabs {
		tabs, err := remote.TabList(*filter)
		if err != nil {
			log.Fatal("cannot get list of tabs: ", err)
		}

		pretty.PrettyPrint(tabs)
		should_wait = false
	}

	if *domains {
		d, err := remote.GetDomains()
		if err != nil {
			log.Fatal("cannot get domains: ", err)
		}

		pretty.PrettyPrint(d)
		should_wait = false
	}

	remote.CallbackEvent(godet.EventClosed, func(params godet.Params) {
		log.Println("RemoteDebugger connection terminated.")
		done <- true
	})

	if *requests {
		remote.CallbackEvent("Network.requestWillBeSent", func(params godet.Params) {
			log.Println("requestWillBeSent",
				params["type"],
				params["documentURL"],
				params["request"].(map[string]interface{})["url"])
		})
	}

	if *responses {
		remote.CallbackEvent("Network.responseReceived", func(params godet.Params) {
			resp := params["response"].(map[string]interface{})
			url := resp["url"].(string)

			log.Println("responseReceived",
				params["type"],
				limit(url, 80),
				"\n\t\t\t",
				int(resp["status"].(float64)),
				resp["mimeType"].(string))

			/*
				if params["type"].(string) == "Image" {
					go func() {
						req := params["requestId"].(string)
						res, err := remote.GetResponseBody(req)
						if err != nil {
							log.Println("Error getting responseBody", err)
						} else {
							log.Println("ResponseBody", len(res), limit(string(res), 10))
						}
					}()
				}
			*/
		})
	}

	if *logev {
		remote.CallbackEvent("Log.entryAdded", func(params godet.Params) {
			entry := params["entry"].(map[string]interface{})
			log.Println("LOG", entry["type"], entry["level"], entry["text"])
		})

		remote.CallbackEvent("Runtime.consoleAPICalled", func(params godet.Params) {
			l := []interface{}{"CONSOLE", params["type"].(string)}

			for _, a := range params["args"].([]interface{}) {
				arg := a.(map[string]interface{})

				if arg["value"] != nil {
					l = append(l, arg["value"])
				} else if arg["preview"] != nil {
					arg := arg["preview"].(map[string]interface{})

					v := arg["description"].(string) + "{"

					for i, p := range arg["properties"].([]interface{}) {
						if i > 0 {
							v += ", "
						}

						prop := p.(map[string]interface{})
						if prop["name"] != nil {
							v += fmt.Sprintf("%q: ", prop["name"])
						}

						v += fmt.Sprintf("%v", prop["value"])
					}

					v += "}"
					l = append(l, v)
				} else {
					l = append(l, arg["type"].(string))
				}

			}

			log.Println(l...)
		})
	}

	if *control != "" {
		remote.SetControlNavigations(true)
		navigationResponse := godet.NavigationProceed

		switch *control {
		case "proceed":
			navigationResponse = godet.NavigationProceed
		case "cancel":
			navigationResponse = godet.NavigationCancel
		case "cancelIgnore":
			navigationResponse = godet.NavigationCancelAndIgnore
		}

		remote.CallbackEvent("Page.navigationRequested", func(params godet.Params) {
			log.Println("navigation requested for", params.String("url"), navigationResponse)

			remote.ProcessNavigation(params.Int("navigationId"), navigationResponse)
		})
	}

	if *block != "" {
		blocks := strings.Split(*block, "|")
		remote.SetBlockedURLs(blocks...)
	}

	if *screenshot {
		remote.CallbackEvent("DOM.documentUpdated", func(params godet.Params) {
			log.Println("document updated. taking screenshot...")
			remote.SaveScreenshot("screenshot.png", 0644, 0, true)

			done <- true
		})
	}

	if *pdf {
		remote.CallbackEvent("DOM.documentUpdated", func(params godet.Params) {
			log.Println("document updated. saving as PDF...")
			remote.SavePDF("page.pdf", 0644)

			done <- true
		})
	}

	var site string

	if flag.NArg() > 0 {
		site = flag.Arg(0)

		tabs, err := remote.TabList("page")
		if err != nil {
			log.Fatal("cannot get tabs: ", err)
		}

		if len(tabs) == 0 || *newtab {
			_, err = remote.NewTab(site)
			site = ""
		} else {
			tab := *seltab
			if tab > len(tabs) {
				tab = 0
			}

			err = remote.ActivateTab(tabs[tab])
		}

		if err != nil {
			log.Fatal("error loading page: ", err)
		}
	}

	//
	// enable events AFTER creating/selecting a tab but BEFORE navigating to a page
	//
	if *allEvents {
		remote.AllEvents(true)
	} else {
		remote.RuntimeEvents(true)
		remote.NetworkEvents(true)
		remote.PageEvents(true)
		remote.DOMEvents(true)
		remote.LogEvents(true)
	}

	if len(site) > 0 {
		_, err = remote.Navigate(site)
		if err != nil {
			log.Fatal("error loading page: ", err)
		}
	}

	if *query != "" {
		id := documentNode(remote, *verbose)

		res, err := remote.QuerySelector(id, *query)
		if err != nil {
			log.Fatal("error in querySelector: ", err)
		}

		if res == nil {
			log.Println("no result for", *query)
		} else {
			id = int(res["nodeId"].(float64))
			res, err = remote.ResolveNode(id)
			if err != nil {
				log.Fatal("error in resolveNode: ", err)
			}

			pretty.PrettyPrint(res)
		}

		should_wait = false
	}

	if *eval != "" {
		res, err := remote.EvaluateWrap(*eval)
		if err != nil {
			log.Fatal("error in evaluate: ", err)
		}

		pretty.PrettyPrint(res)
		should_wait = false
	}

	if *setHtml != "" {
		id := documentNode(remote, *verbose)

		res, err := remote.QuerySelector(id, "html")
		if err != nil {
			log.Fatal("error in querySelector: ", err)
		}

		id = int(res["nodeId"].(float64))

		err = remote.SetOuterHTML(id, *setHtml)
		if err != nil {
			log.Fatal("error in setOuterHTML: ", err)
		}

		should_wait = false
	}

	if *html {
		id := documentNode(remote, *verbose)

		res, err := remote.GetOuterHTML(id)
		if err != nil {
			log.Fatal("error in getOuterHTML: ", err)
		}

		log.Println(res)
		should_wait = false
	}

	if *wait || should_wait {
		log.Println("Wait for events...")
		<-done
	}

	log.Println("Closing")
}
