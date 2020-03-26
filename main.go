package main

import (
	"expvar"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/flexd/slackinviter/slack"

	"github.com/gorilla/handlers"
	"github.com/kelseyhightower/envconfig"
	"github.com/paulbellamy/ratecounter"
)

var (
	inviter *slack.Inviter
	counter *ratecounter.RateCounter

	m *expvar.Map
	hitsPerMinute,
	requests,
	missingFirstName,
	missingLastName,
	missingEmail,
	missingCoC,
	successfulCaptcha,
	failedCaptcha expvar.Int
)

var c Specification

// Specification is the config struct
type Specification struct {
	slack.InviterConfig
	Port         string `envconfig:"PORT" required:"true"`
	EnforceHTTPS bool
}

func init() {
	var showUsage = flag.Bool("h", false, "Show usage")
	flag.Parse()

	if *showUsage {
		err := envconfig.Usage("slackinviter", &c)
		if err != nil {
			log.Fatal(err.Error())
		}
		os.Exit(0)
	}

	err := envconfig.Process("slackinviter", &c)
	if err != nil {
		log.Fatal(err.Error())
	}
	counter = ratecounter.NewRateCounter(1 * time.Minute)
	m = expvar.NewMap("metrics")
	m.Set("hits_per_minute", &hitsPerMinute)
	m.Set("requests", &requests)
	m.Set("invite_errors", &slack.InviteErrors)
	m.Set("missing_first_name", &missingFirstName)
	m.Set("missing_last_name", &missingLastName)
	m.Set("missing_email", &missingEmail)
	m.Set("missing_coc", &missingCoC)
	m.Set("failed_captcha", &slack.FailedCaptcha)
	m.Set("invalid_captcha", &slack.InvalidCaptcha)
	m.Set("successful_captcha", &successfulCaptcha)
	m.Set("successful_invites", &slack.SuccessfulInvites)
	m.Set("active_user_count", &slack.ActiveUserCount)
	m.Set("user_count", &slack.UserCount)
}

func handleBadge(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	inviter.RenderBadge(w)
}

func main() {
	go pollSlack()

	inviter = slack.NewInviter(c.InviterConfig)
	mux := http.NewServeMux()
	mux.HandleFunc("/invite/", handleInvite)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))
	mux.HandleFunc("/", enforceHTTPSFunc(homepage))
	mux.HandleFunc("/badge.svg", handleBadge)
	mux.Handle("/debug/vars", http.DefaultServeMux)
	err := http.ListenAndServe(":"+c.Port, handlers.CombinedLoggingHandler(os.Stdout, mux))
	if err != nil {
		log.Fatal(err.Error())
	}
}

func enforceHTTPSFunc(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if xfp := r.Header.Get("X-Forwarded-Proto"); c.EnforceHTTPS && xfp == "http" {
			u := *r.URL
			u.Scheme = "https"
			if u.Host == "" {
				u.Host = r.Host
			}
			http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
			return
		}
		h(w, r)
	}
}

// pollSlack over and over again
func pollSlack() {
	for {
		time.Sleep(inviter.UpdateFromSlack())
	}
}

// Homepage renders the homepage
func homepage(w http.ResponseWriter, r *http.Request) {
	counter.Incr(1)
	hitsPerMinute.Set(counter.Rate())
	requests.Add(1)

	inviter.RenderHomepage(w)
}

// ShowPost renders a single post
func handleInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	successfulCaptcha.Add(1)
	fname := r.FormValue("fname")
	lname := r.FormValue("lname")
	email := r.FormValue("email")
	coc := r.FormValue("coc")
	if email == "" {
		missingEmail.Add(1)
		http.Error(w, "Missing email", http.StatusPreconditionFailed)
		return
	}
	if fname == "" {
		missingFirstName.Add(1)
		http.Error(w, "Missing first name", http.StatusPreconditionFailed)
		return
	}
	if lname == "" {
		missingLastName.Add(1)
		http.Error(w, "Missing last name", http.StatusPreconditionFailed)
		return
	}
	if coc != "1" {
		missingCoC.Add(1)
		http.Error(w, "You need to accept the code of conduct", http.StatusPreconditionFailed)
		return
	}
	remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		failedCaptcha.Add(1)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	captchaResponse := r.FormValue("g-recaptcha-response")
	err = inviter.Invite(fname, lname, email, captchaResponse, remoteIP)
	if err != nil {
		if err == slack.ErrValidatingCatchpta {
			http.Error(w, err.Error(), http.StatusPreconditionFailed)
			return
		}

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
