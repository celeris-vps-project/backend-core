package messageclient

import (
	"backend-core/pkg/messagepb"
	"context"
	"fmt"
	"strings"
)

type Sender interface {
	Send(ctx context.Context, req *messagepb.SendRequest) (*messagepb.SendResponse, error)
}

type UserRegisteredNotifierConfig struct {
	Enabled      bool
	Channel      string
	Subject      string
	Content      string
	TemplateCode string
}

type UserRegisteredNotifier struct {
	sender Sender
	config UserRegisteredNotifierConfig
}

func NewUserRegisteredNotifier(sender Sender, config UserRegisteredNotifierConfig) *UserRegisteredNotifier {
	if strings.TrimSpace(config.Channel) == "" {
		config.Channel = "IN_APP"
	}
	if strings.TrimSpace(config.Subject) == "" {
		config.Subject = "Welcome to Celeris"
	}
	if strings.TrimSpace(config.Content) == "" {
		config.Content = "Your account has been created successfully."
	}
	return &UserRegisteredNotifier{
		sender: sender,
		config: config,
	}
}

func (n *UserRegisteredNotifier) NotifyUserRegistered(ctx context.Context, userID, email string) error {
	if n == nil || !n.config.Enabled {
		return nil
	}
	if n.sender == nil {
		return fmt.Errorf("message sender is nil")
	}

	channel := strings.ToUpper(strings.TrimSpace(n.config.Channel))
	recipient, err := resolveRecipient(channel, userID, email)
	if err != nil {
		return err
	}

	req := &messagepb.SendRequest{
		Channel:   channel,
		Recipient: recipient,
		Subject:   n.config.Subject,
		BizId:     "identity.user_registered:" + strings.TrimSpace(userID),
	}
	if templateCode := strings.TrimSpace(n.config.TemplateCode); templateCode != "" {
		req.TemplateCode = templateCode
	} else {
		req.Content = n.config.Content
	}

	resp, err := n.sender.Send(ctx, req)
	if err != nil {
		return err
	}
	if !resp.GetSuccess() {
		return fmt.Errorf("message service send failed: %s", resp.GetErrorMsg())
	}
	return nil
}

func resolveRecipient(channel, userID, email string) (string, error) {
	switch channel {
	case "IN_APP":
		if strings.TrimSpace(userID) == "" {
			return "", fmt.Errorf("userID must not be blank for IN_APP notifications")
		}
		return strings.TrimSpace(userID), nil
	case "EMAIL":
		if strings.TrimSpace(email) == "" {
			return "", fmt.Errorf("email must not be blank for EMAIL notifications")
		}
		return strings.TrimSpace(email), nil
	default:
		return "", fmt.Errorf("unsupported user_registered channel: %s", channel)
	}
}
