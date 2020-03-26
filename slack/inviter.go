package slack

import (
	"bytes"
	"context"
	"errors"
	"expvar"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/narqo/go-badge"

	"github.com/go-recaptcha/recaptcha"

	"github.com/nlopes/slack"
)

var indexTemplate = template.Must(template.New("index.tmpl").ParseFiles("slack/templates/index.tmpl"))

var ErrValidatingCatchpta = errors.New("Error validating recaptcha.. Did you click it?")

var (
	FailedCaptcha,
	InvalidCaptcha,
	SuccessfulInvites,
	InviteErrors,
	ActiveUserCount,
	UserCount expvar.Int
)

type Inviter struct {
	api     *slack.Client
	captcha *recaptcha.Recaptcha
	c       InviterConfig
}

var (
	ourTeam = new(team)
)

type InviterConfig struct {
	CaptchaSitekey string `required:"true"`
	CaptchaSecret  string `required:"true"`
	SlackToken     string `required:"true"`
	CocUrl         string `required:"false" default:"http://coc.golangbridge.org/"`
	Debug          bool   // toggles nlopes/slack client's debug flag
}

func NewInviter(c InviterConfig) *Inviter {
	return &Inviter{
		api:     slack.New(c.SlackToken, slack.OptionDebug(c.Debug)),
		captcha: recaptcha.New(c.CaptchaSecret),
		c:       c,
	}
}

func (i *Inviter) Invite(fname, lname, email, captchaResponse, remoteIP string) error {
	valid, err := i.captcha.Verify(captchaResponse, remoteIP)
	if err != nil {
		FailedCaptcha.Add(1)
		return ErrValidatingCatchpta
	}
	if !valid {
		InvalidCaptcha.Add(1)
		return errors.New("Invalid recaptcha")

	}
	// all is well, let's try to invite someone!
	err = i.api.InviteToTeam(ourTeam.Domain(), fname, lname, email)
	if err != nil {
		log.Println("InviteToTeam error:", err)
		InviteErrors.Add(1)
		return err
	}
	SuccessfulInvites.Add(1)
	return nil
}

func (i *Inviter) RenderBadge(w http.ResponseWriter) {
	users := UserCount.String()
	if ActiveUserCount.Value() > 0 {
		users = ActiveUserCount.String() + "/" + UserCount.String()
	}

	var buf bytes.Buffer
	if err := badge.Render("slack", users, "#E01563", &buf); err != nil {
		log.Fatal(err)
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	buf.WriteTo(w)
}

func (i *Inviter) RenderHomepage(w http.ResponseWriter) {
	var buf bytes.Buffer
	err := indexTemplate.Execute(
		&buf,
		struct {
			SiteKey,
			UserCount,
			ActiveCount string
			Team   *team
			CocUrl string
		}{
			i.c.CaptchaSitekey,
			UserCount.String(),
			ActiveUserCount.String(),
			ourTeam,
			i.c.CocUrl,
		},
	)
	if err != nil {
		log.Println("error rendering template:", err)
		http.Error(w, "error rendering template :-(", http.StatusInternalServerError)
		return
	}
	// Set the header and write the buffer to the http.ResponseWriter
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

// Updates the globals from the slack API
// returns the length of time to sleep before the function
// should be called again
func (i *Inviter) UpdateFromSlack() time.Duration {
	var (
		err            error
		p              slack.UserPagination
		uCount, aCount int64 // users and active users
	)

	ctx := context.Background()
	for p = i.api.GetUsersPaginated(
		slack.GetUsersOptionPresence(true),
		slack.GetUsersOptionLimit(500),
	); !p.Done(err); p, err = p.Next(ctx) {
		if err != nil {
			if rle, ok := err.(*slack.RateLimitedError); ok {
				fmt.Printf("Being Rate Limited by Slack: %s\n", rle)
				time.Sleep(rle.RetryAfter)
				continue
			}
		}
		for _, u := range p.Users {
			if u.ID != "USLACKBOT" && !u.IsBot && !u.Deleted {
				uCount++
				if u.Presence == "active" {
					aCount++
				}
			}
		}
		fmt.Println("User Count:", uCount)
		fmt.Println("Active Count:", aCount)
	}
	UserCount.Set(uCount)
	ActiveUserCount.Set(aCount)
	if err != nil && !p.Done(err) {
		log.Println("error polling slack for users:", err)
		return time.Minute
	}

	st, err := i.api.GetTeamInfo()
	if err != nil {
		log.Println("error polling slack for team info:", err)
		return time.Minute
	}
	ourTeam.Update(st)
	return time.Hour
}
