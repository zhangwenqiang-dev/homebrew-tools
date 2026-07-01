package connectmac

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const webSessionCookie = "cm_session"

type webAuthMember struct {
	Member        PublicMember `json:"member,omitempty"`
	Authenticated bool         `json:"authenticated"`
	SetupRequired bool         `json:"setup_required"`
}

func (a App) webAuthMeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		setupRequired, err := a.webSetupRequired()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		member, ok := a.currentWebMember(r)
		resp := webAuthMember{Authenticated: ok, SetupRequired: setupRequired}
		if ok {
			resp.Member = publicMember(member)
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: resp})
	}
}

func (a App) webAuthChallengeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		challenge, err := a.newWebChallenge()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: challenge})
	}
}

func (a App) webAuthSetupHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		setupRequired, err := a.webSetupRequired()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !setupRequired {
			writeWebError(w, http.StatusForbidden, "admin is already configured")
			return
		}
		var req struct {
			Name            string `json:"name"`
			Email           string `json:"email"`
			Password        string `json:"password"`
			ChallengeToken  string `json:"challenge_token"`
			ChallengeAnswer string `json:"challenge_answer"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if err := a.verifyWebChallenge(req.ChallengeToken, req.ChallengeAnswer); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		member, err := a.MemberStore.SetupAdmin(req.Name, req.Email, req.Password)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.setWebSession(w, member); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: webAuthMember{Authenticated: true, Member: publicMember(member)}})
	}
}

func (a App) webAuthLoginHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Username        string `json:"username"`
			Password        string `json:"password"`
			ChallengeToken  string `json:"challenge_token"`
			ChallengeAnswer string `json:"challenge_answer"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if err := a.verifyWebChallenge(req.ChallengeToken, req.ChallengeAnswer); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		member, ok, err := a.MemberStore.VerifyMemberPassword(req.Username, req.Password)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeWebError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		if err := a.setWebSession(w, member); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: webAuthMember{Authenticated: true, Member: publicMember(member)}})
	}
}

func (a App) webAuthLogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: webSessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		writeWebJSON(w, webAPIResponse{OK: true})
	}
}

func (a App) webSettingsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			settings, err := a.MemberStore.WebSettings()
			if err != nil {
				writeWebError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"settings": settings}})
		case http.MethodPost:
			if _, ok := a.requireWebRoleValue(r, "admin"); !ok {
				writeWebError(w, http.StatusForbidden, "admin login required")
				return
			}
			var req WebSettings
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeWebError(w, http.StatusBadRequest, "invalid json body")
				return
			}
			settings, err := a.MemberStore.UpdateWebSettings(req)
			if err != nil {
				writeWebError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"settings": settings}})
		default:
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

func (a App) requireWebRole(next http.HandlerFunc, roles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.requireWebRoleValue(r, roles...); !ok {
			writeWebError(w, http.StatusUnauthorized, "login required")
			return
		}
		next(w, r)
	}
}

func (a App) requireWebRoleValue(r *http.Request, roles ...string) (Member, bool) {
	member, ok := a.currentWebMember(r)
	if !ok || !member.Enabled {
		return Member{}, false
	}
	for _, role := range roles {
		if member.Role == role {
			return member, true
		}
	}
	return Member{}, false
}

func (a App) currentWebMember(r *http.Request) (Member, bool) {
	cookie, err := r.Cookie(webSessionCookie)
	if err != nil || cookie.Value == "" {
		return Member{}, false
	}
	email, ok := a.verifyWebSession(cookie.Value)
	if !ok {
		return Member{}, false
	}
	db, err := a.MemberStore.Load()
	if err != nil {
		return Member{}, false
	}
	member, ok := findMemberByEmailOrUsername(db, email)
	if !ok || !member.Enabled {
		return Member{}, false
	}
	return member, true
}

func (a App) webSetupRequired() (bool, error) {
	hasMembers, err := a.MemberStore.HasPasswordMembers()
	if err != nil {
		return false, err
	}
	return !hasMembers, nil
}

func (a App) setWebSession(w http.ResponseWriter, member Member) error {
	secret, err := a.MemberStore.EnsureAuthSecret()
	if err != nil {
		return err
	}
	expires := time.Now().Add(12 * time.Hour).Unix()
	payload := normalizeEmail(member.Email) + "|" + strconv.FormatInt(expires, 10)
	sig := signWebValue(secret, payload)
	http.SetCookie(w, &http.Cookie{
		Name:     webSessionCookie,
		Value:    base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + sig)),
		Path:     "/",
		Expires:  time.Unix(expires, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (a App) verifyWebSession(value string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return "", false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > expires {
		return "", false
	}
	secret, err := a.MemberStore.EnsureAuthSecret()
	if err != nil {
		return "", false
	}
	payload := parts[0] + "|" + parts[1]
	expected := signWebValue(secret, payload)
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return "", false
	}
	return parts[0], true
}

func (a App) newWebChallenge() (map[string]string, error) {
	secret, err := a.MemberStore.EnsureAuthSecret()
	if err != nil {
		return nil, err
	}
	left, err := randomSmallInt()
	if err != nil {
		return nil, err
	}
	right, err := randomSmallInt()
	if err != nil {
		return nil, err
	}
	answer := strconv.Itoa(left + right)
	nonce, err := randomToken(12)
	if err != nil {
		return nil, err
	}
	expires := time.Now().Add(5 * time.Minute).Unix()
	payload := nonce + "|" + answer + "|" + strconv.FormatInt(expires, 10)
	token := base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + signWebValue(secret, payload)))
	return map[string]string{
		"question": fmt.Sprintf("%d + %d = ?", left, right),
		"token":    token,
	}, nil
}

func (a App) verifyWebChallenge(token, answer string) error {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return errors.New("invalid challenge")
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 {
		return errors.New("invalid challenge")
	}
	expires, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().Unix() > expires {
		return errors.New("challenge expired")
	}
	secret, err := a.MemberStore.EnsureAuthSecret()
	if err != nil {
		return err
	}
	payload := parts[0] + "|" + parts[1] + "|" + parts[2]
	if !hmac.Equal([]byte(signWebValue(secret, payload)), []byte(parts[3])) {
		return errors.New("invalid challenge")
	}
	if strings.TrimSpace(answer) != parts[1] {
		return errors.New("challenge answer is incorrect")
	}
	return nil
}

func signWebValue(secret, value string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func randomSmallInt() (int, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(9))
	if err != nil {
		return 0, err
	}
	return int(n.Int64()) + 1, nil
}
