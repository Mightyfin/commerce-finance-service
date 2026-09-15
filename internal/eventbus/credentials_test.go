package eventbus

import "testing"

func TestEventCredentialsFailClosed(t *testing.T) {
	for _, v := range [][3]string{{"", "", ""}, {"", "user", ""}, {"", "", "password"}, {"token", "user", "password"}} {
		if _, err := ConnectionCredentials(v[0], v[1], v[2]); err == nil {
			t.Fatal("invalid credentials accepted")
		}
	}
}
