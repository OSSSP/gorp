// Package debugger provides functionality for using Chrome and the Chrome Dev Tools protocol
package debugger

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/DharmaOfCode/gorp/modules"
	"github.com/wirepair/gcd"
	"github.com/wirepair/gcd/gcdapi"
	"log"
	"strconv"
	"strings"
	"time"
)

// Debugger holds the configuration for the Chrome Dev Protocol hooks. It also
// contains modules to be used as requests and responses are intercepted.
type Debugger struct {
	ChromeProxy *gcd.Gcd
	Done        chan bool
	Options     Options
	Target      *gcd.ChromeTarget
	Modules     modules.Modules
}

// Options defines the options used with the debugger, which is responsible for using the Chrome Dev Tools
// protocol
type Options struct {
	EnableConsole bool
	Verbose       bool
	Scope         string
}

// StartTarget initializes  Chrome and sets up the Chrome Dev Tools protocol targets so that events can be intercepted
func (d *Debugger) StartTarget() {
	target, err := d.ChromeProxy.NewTab()
	if err != nil {
		log.Fatalf("error getting new tab: %s\n", err)
	}

	target.DebugEvents(d.Options.Verbose)
	target.DOM.Enable()
	target.Console.Enable()
	target.Page.Enable()
	target.Debugger.Enable()
	networkParams := &gcdapi.NetworkEnableParams{
		MaxTotalBufferSize:    -1,
		MaxResourceBufferSize: -1,
	}
	if _, err := target.Network.EnableWithParams(networkParams); err != nil {
		log.Fatal("[-] Error enabling network!")
	}
	d.Target = target
}

// SetupRequestInterception enables request interception using the specific params
func (d *Debugger) SetupRequestInterception(params *gcdapi.NetworkSetRequestInterceptionParams) {
	log.Println("[+] Setting up request interception")
	if _, err := d.Target.Network.SetRequestInterceptionWithParams(params); err != nil {
		log.Println("[-] Unable to setup request interception!", err)
	}

	d.Target.Subscribe("Network.requestIntercepted", func(target *gcd.ChromeTarget, v []byte) {

		msg := &gcdapi.NetworkRequestInterceptedEvent{}
		err := json.Unmarshal(v, msg)
		if err != nil {
			log.Fatalf("error unmarshalling event data: %v\n", err)
		}
		iid := msg.Params.InterceptionId
		reason := msg.Params.ResponseErrorReason
		rtype := msg.Params.ResourceType
		responseHeaders := msg.Params.ResponseHeaders
		url := msg.Params.Request.Url

		if msg.Params.IsNavigationRequest {
			log.Print("\n\n\n\n")
			log.Println("[?] Navigation REQUEST")
		}
		log.Println("[+] Request intercepted for", url)
		if reason != "" {
			log.Println("[-] Abort with reason", reason)
		}

		if iid != "" {
			res, encoded, err := d.Target.Network.GetResponseBodyForInterception(iid)
			if err != nil {
				log.Println("[-] Unable to get intercepted response body!", err.Error())
				target.Network.ContinueInterceptedRequest(iid, reason, "", "", "", "", nil, nil)
			} else {
				if encoded {
					res, err = decodeBase64Response(res)
					if err != nil {
						log.Println("[-] Unable to decode body!")
					}
				}
				webData := modules.WebData{
					Body:    res,
					Headers: responseHeaders,
					Type:    rtype,
					Url:     url,
				}
				go d.CallInspectors(webData)

				if rtype != "" {
					rawAlteredResponse, err := d.CallProcessors(webData)
					if err != nil {
						log.Println("[-] Unable to alter HTML")
					}

					log.Print("[+] Sending modified body\n\n\n")

					_, err = d.Target.Network.ContinueInterceptedRequest(iid, reason, rawAlteredResponse, "", "", "", nil, nil)
					if err != nil {
						log.Println(err)
					}
				} else {
					d.Target.Network.ContinueInterceptedRequest(iid, reason, "", "", "", "", nil, nil)
				}
			}
		} else {
			d.Target.Network.ContinueInterceptedRequest(iid, reason, "", "", "", "", nil, nil)
		}
	})
}

// CallProcessors alters the body of web responses using the selected processors
func (d *Debugger) CallProcessors(data modules.WebData) (string, error) {
	alteredBody, err := d.processBody(data)
	if err != nil {
		return "", err
	}

	alteredHeader := ""
	for k, v := range data.Headers {
		switch strings.ToLower(k) {
		case "content-length":
			v = strconv.Itoa(len(alteredBody))
			break
		case "date":
			v = fmt.Sprintf("%s", time.Now().Format(time.RFC3339))
			break
		}
		alteredHeader += k + ": " + v.(string) + "\r\n"
	}
	alteredHeader += "\r\n"

	rawAlteredResponse := base64.StdEncoding.EncodeToString([]byte("HTTP/1.1 200 OK" + "\r\n" + alteredHeader + alteredBody))

	return rawAlteredResponse, nil
}

// CallInspectors executes inspectors in a gorp session
func (d *Debugger) CallInspectors(webData modules.WebData) {
	//TODO: abstract this as a debugger function
	for _, v := range d.Modules.Inspectors {
		//TODO call all inspectors as goroutines
		err := v.Inspect(webData)
		if err != nil {
			log.Println("[+] Inspector error: " + v.Registry.Name)
		}
	}
}

func decodeBase64Response(res string) (string, error) {
	l, err := base64.StdEncoding.DecodeString(res)
	if err != nil {
		return "", err
	}

	return string(l[:]), nil
}

func (d *Debugger) processBody(data modules.WebData) (string, error) {
	result := data
	var err error
	for _, v := range d.Modules.Processors {
		log.Println("[+] Running processor: " + v.Registry.Name)
		result.Body, err = v.Process(result)
		if err != nil {
			return "", err
		}
	}
	return result.Body, nil
}
