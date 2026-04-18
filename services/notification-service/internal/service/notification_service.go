// Package service contains notification use-case logic.
package service

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"log"
	"strings"

	"banka-backend/services/notification-service/internal/config"
	"banka-backend/services/notification-service/internal/domain"
	"banka-backend/services/notification-service/internal/smtp"
)

//go:embed templates/activation.html
var activationTmpl string

//go:embed templates/reset.html
var resetTmpl string

//go:embed templates/activation_success.html
var activationSuccessTmpl string

//go:embed templates/password_reset_success.html
var passwordResetSuccessTmpl string

//go:embed templates/account_created.html
var accountCreatedTmpl string

//go:embed templates/card_otp.html
var cardOTPTmpl string

//go:embed templates/card_status_changed.html
var cardStatusChangedTmpl string

//go:embed templates/card_created.html
var cardCreatedTmpl string

//go:embed templates/kredit_podnet.html
var kreditPodnetTmpl string

//go:embed templates/kredit_rata_upozorenje.html
var kreditRataUpozorenjeTmpl string

// emailTmplEntry holds a pre-parsed template and its email subject line.
type emailTmplEntry struct {
	subject string
	tmpl    *template.Template
}

// EmailService sends transactional emails via an injected smtp.Sender.
// Recipient is always taken from the event (e.g. from frontend/request payload).
type EmailService struct {
	cfg    *config.Config
	sender smtp.Sender
	tmpls  map[string]emailTmplEntry
}

// NewEmailService returns a ready EmailService.
// All HTML templates are parsed once here — template.Must panics on malformed
// embedded HTML, which is a programming error caught immediately at startup.
func NewEmailService(cfg *config.Config, sender smtp.Sender) *EmailService {
	must := func(name, src string) *template.Template {
		return template.Must(template.New(name).Parse(src))
	}
	return &EmailService{
		cfg:    cfg,
		sender: sender,
		tmpls: map[string]emailTmplEntry{
			"ACTIVATION":             {subject: "Activate Your EXBanka2 Account", tmpl: must("activation", activationTmpl)},
			"RESET":                  {subject: "Password Reset Request", tmpl: must("reset", resetTmpl)},
			"ACTIVATION_SUCCESS":     {subject: "Welcome to EXBanka2 \u2013 Your Account is Now Active", tmpl: must("activation_success", activationSuccessTmpl)},
			"PASSWORD_RESET_SUCCESS": {subject: "Security Alert: Your EXBanka2 Password Has Been Changed", tmpl: must("password_reset_success", passwordResetSuccessTmpl)},
			"ACCOUNT_CREATED":        {subject: "Your EXBanka2 Account Has Been Created", tmpl: must("account_created", accountCreatedTmpl)},
			"CARD_OTP":               {subject: "EXBanka2 \u2014 Card Verification Code", tmpl: must("card_otp", cardOTPTmpl)},
			"CARD_STATUS_CHANGED":    {subject: "EXBanka2 \u2014 Card Status Update", tmpl: must("card_status_changed", cardStatusChangedTmpl)},
			"KREIRANA_KARTICA":       {subject: "EXBanka2 \u2014 Nova platna kartica kreirana", tmpl: must("card_created", cardCreatedTmpl)},
			"KREDIT_PODNET":          {subject: "EXBanka2 \u2014 Zahtev za kredit primljen", tmpl: must("kredit_podnet", kreditPodnetTmpl)},
			"KREDIT_RATA_UPOZORENJE": {subject: "EXBanka2 \u2014 Upozorenje o naplati rate kredita", tmpl: must("kredit_rata_upozorenje", kreditRataUpozorenjeTmpl)},
		},
	}
}

// SendEmail dispatches an HTML email based on the event type.
// Returns domain.ErrUnknownEventType for unrecognized types so callers can
// distinguish client errors from infrastructure failures.
func (s *EmailService) SendEmail(event domain.EmailEvent) error {
	recipient := strings.TrimSpace(event.Email)
	if recipient == "" {
		return fmt.Errorf("recipient email is required")
	}

	entry, ok := s.tmpls[event.Type]
	if !ok {
		return domain.ErrUnknownEventType{Type: event.Type}
	}

	var body bytes.Buffer
	if err := entry.tmpl.Execute(&body, struct {
		FrontendURL string
		Token       string
	}{
		FrontendURL: s.cfg.FrontendURL,
		Token:       event.Token,
	}); err != nil {
		return fmt.Errorf("template execute: %w", err)
	}

	if err := s.sender.Send(recipient, entry.subject, body.String()); err != nil {
		log.Printf("[notification] send email failed type=%s recipient=%s: %v", event.Type, recipient, err)
		return err
	}
	log.Printf("[notification] email sent type=%s to %s", event.Type, recipient)
	return nil
}
