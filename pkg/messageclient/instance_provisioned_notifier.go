package messageclient

import (
	"backend-core/pkg/messagepb"
	"context"
	"fmt"
	"strings"
)

type InstanceProvisionedMessage struct {
	UserID          string
	InstanceID      string
	Hostname        string
	IPv4            string
	HostIP          string
	NetworkMode     string
	NATPort         int
	InitialPassword string
}

type InstanceProvisionedNotifier struct {
	sender Sender
}

func NewInstanceProvisionedNotifier(sender Sender) *InstanceProvisionedNotifier {
	return &InstanceProvisionedNotifier{sender: sender}
}

func (n *InstanceProvisionedNotifier) NotifyInstanceProvisioned(ctx context.Context, msg InstanceProvisionedMessage) error {
	if n == nil || n.sender == nil {
		return nil
	}
	userID := strings.TrimSpace(msg.UserID)
	if userID == "" {
		return fmt.Errorf("userID must not be blank for instance notifications")
	}

	access := msg.IPv4
	if strings.EqualFold(msg.NetworkMode, "nat") {
		access = msg.HostIP
		if msg.NATPort > 0 {
			access = fmt.Sprintf("%s:%d", msg.HostIP, msg.NATPort)
		}
	}

	content := fmt.Sprintf(
		"Instance %s is ready.\nAccess: %s\nSSH user: root\nInitial password: %s",
		msg.Hostname,
		access,
		msg.InitialPassword,
	)
	req := &messagepb.SendRequest{
		Channel:   "IN_APP",
		Recipient: userID,
		Subject:   "Instance is ready",
		Content:   content,
		BizId:     "instance.provisioned:" + strings.TrimSpace(msg.InstanceID),
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
