package eventbus

import (
	"fmt"
	"github.com/nats-io/nats.go"
	"strings"
)

func ConnectionCredentials(token, user, password string) ([]nats.Option, error) {
	token, user = strings.TrimSpace(token), strings.TrimSpace(user)
	if user != "" || password != "" {
		if token != "" || user == "" || password == "" {
			return nil, fmt.Errorf("event credentials incomplete or ambiguous")
		}
		return []nats.Option{nats.UserInfo(user, password)}, nil
	}
	if token != "" {
		return []nats.Option{nats.Token(token)}, nil
	}
	return nil, fmt.Errorf("event credentials required")
}
