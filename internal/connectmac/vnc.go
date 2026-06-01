package connectmac

import (
	"fmt"
	"net/url"
	"strconv"
)

func VNCURL(profile Profile) (string, error) {
	if len(profile.Tunnels) == 0 {
		return "", fmt.Errorf("profile %s has no tunnels configured", profile.Name)
	}
	port := profile.Tunnels[0].LocalPort
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("profile %s has invalid VNC local port %d", profile.Name, port)
	}
	u := url.URL{
		Scheme: "vnc",
		Host:   "localhost:" + strconv.Itoa(port),
	}
	if profile.VNC.Username != "" {
		u.User = url.User(profile.VNC.Username)
	}
	return u.String(), nil
}
