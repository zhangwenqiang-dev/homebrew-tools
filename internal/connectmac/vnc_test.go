package connectmac

import "testing"

func TestVNCURLWithoutUsername(t *testing.T) {
	profile := validProfile("/tmp/key.pem")
	profile.VNC.Username = ""
	got, err := VNCURL(profile)
	if err != nil {
		t.Fatalf("VNCURL returned error: %v", err)
	}
	if got != "vnc://localhost:5900" {
		t.Fatalf("url = %q", got)
	}
}

func TestVNCURLWithUsername(t *testing.T) {
	profile := validProfile("/tmp/key.pem")
	profile.VNC.Username = "mac-user"
	got, err := VNCURL(profile)
	if err != nil {
		t.Fatalf("VNCURL returned error: %v", err)
	}
	if got != "vnc://mac-user@localhost:5900" {
		t.Fatalf("url = %q", got)
	}
}
