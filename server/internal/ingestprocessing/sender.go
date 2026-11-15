package ingestprocessing

import (
	"context"
	"errors"
)

var ErrSenderUnavailable = errors.New("staged batch sender cannot handle every retained occurrence; no attempt started")

// Package-private preflight is implemented only by built-in one-attempt adapters.
// It is pure validation, not provider I/O, authorization or a delivery promise.
// Arbitrary external Sender implementations cannot supply this private method.
type intentPreflight interface{ validateIntents([]Intent) error }

type notificationSender struct {
	smtp  *smtpSender
	slack *slackSender
}

// NewNotificationSender builds one explicit default-off SMTP/Slack router.
// Nil SMTP and empty Slack omit those capabilities. At least one is required.
// Configuration is copied, not loaded from environment, and no network request
// is made. This does not start a worker, activate processing, route a legacy
// notifier or grant permission for any actual message or recipient.
func NewNotificationSender(smtpConfig *SMTPConfig, slackToken string) (Sender, error) {
	s := &notificationSender{}
	if smtpConfig != nil {
		v, err := NewSMTPSender(*smtpConfig)
		if err != nil {
			return nil, err
		}
		s.smtp = v.(*smtpSender)
	}
	if slackToken != "" {
		v, err := NewSlackSender(slackToken)
		if err != nil {
			return nil, err
		}
		s.slack = v.(*slackSender)
	}
	if s.smtp == nil && s.slack == nil {
		return nil, ErrSenderUnavailable
	}
	return s, nil
}
func (s *notificationSender) route(i Intent) (Sender, error) {
	if s == nil {
		return nil, ErrSenderUnavailable
	}
	switch i.ChannelType {
	case "email":
		if s.smtp != nil {
			return s.smtp, nil
		}
	case "slack":
		if s.slack != nil {
			return s.slack, nil
		}
	}
	return nil, ErrSenderUnavailable
}
func (s *notificationSender) validateIntents(intents []Intent) error {
	if s == nil {
		return ErrSenderUnavailable
	}
	for _, i := range intents {
		target, err := s.route(i)
		if err != nil {
			return err
		}
		if err = target.(intentPreflight).validateIntents([]Intent{i}); err != nil {
			return err
		}
	}
	return nil
}
func (s *notificationSender) SendOnce(ctx context.Context, e Envelope, i Intent) error {
	target, err := s.route(i)
	if err != nil {
		return err
	}
	return target.SendOnce(ctx, e, i)
}
