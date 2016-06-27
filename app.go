package main

import (
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"

    _ "github.com/joho/godotenv/autoload"

    "golang.org/x/net/context"
    "google.golang.org/appengine/datastore"
    "google.golang.org/appengine/log"
    "google.golang.org/appengine/user"
)

const (
    EntitySettings           string = "Setting"
    SettingAutofollowEnabled string = "autofollow.enabled"
    SettingAccessToken       string = "instagram.accesstoken"
)
var (
    ErrNoToken     = errors.New("No token")
    ErrNotSignedIn = errors.New("Not signed in")
    ErrNoName      = errors.New("No key")
    ErrNoUserID    = errors.New("No user ID")
)

type Setting struct {
    UserID string  `json:"-"`
    Name   string  `json:"name"`
    Value  string   `json:"value"`
}

func (s *Setting) Delete(ctx context.Context) error {
    key, err := s.Key(ctx)
    if err != nil {
        return err
    }
    if err := datastore.Delete(ctx, key); err != nil {
        return err
    }
    return nil
}

func (s *Setting) Write(w io.Writer) error {
    enc := json.NewEncoder(w)
    if err := enc.Encode(s); err != nil {
        return err
    }
    return nil
}

func (s *Setting) Key(ctx context.Context) (*datastore.Key, error) {
    if err := s.Error(); err != nil {
        return nil, err
    }
    return datastore.NewKey(ctx, EntitySettings, fmt.Sprintf("%s/%s", s.UserID, s.Name), 0, nil), nil
}

func (s *Setting) Error() error {
    if s.UserID == "" {
        return ErrNoUserID
    }
    if s.Name == "" {
        return ErrNoName
    }
    return nil
}

func (s *Setting) Load(ctx context.Context) error {
    key, err := s.Key(ctx)
    if err != nil {
        return err
    }
    err = datastore.Get(ctx, key, s)
    if err != nil {
        return err
    }
    return nil
}

func (s *Setting) Save(ctx context.Context) error {
    key, err := s.Key(ctx)
    if err != nil {
        return err
    }
    if _, err := datastore.Put(ctx, key, s); err != nil {
        return err
    }
    return nil
}

func init() {
    http.HandleFunc("/cron/autofollow", autofollowCronHandler)
    http.HandleFunc("/api/v1/followers/request", followRequestHandler)
    http.HandleFunc("/api/v1/authorize", authorizeBeginHandler)
    http.HandleFunc("/api/v1/authorize/callback", authorizeCallbackHandler)
    http.HandleFunc("/api/v1/user", userHandler)
    http.HandleFunc("/api/v1/token", accessTokenCheckHandler)
    http.HandleFunc("/api/v1/autofollow", enableHandler)
}

func internalServerError(w http.ResponseWriter, ctx context.Context, msg string, err ...interface{}) {
    log.Errorf(ctx, msg, err...)
    code := http.StatusInternalServerError
    http.Error(w, http.StatusText(code), code)
    return
}

func checkUser(w http.ResponseWriter, r *http.Request, ctx context.Context) (*user.User, error) {
    u := user.Current(ctx)
    if u == nil {
        code := http.StatusUnauthorized
        url, _ := user.LoginURL(ctx, "/")
        http.Error(w, url, code)
        return nil, ErrNotSignedIn
    }

    return u, nil
}

func writeJSON(w io.Writer, data interface{}) error {
    enc := json.NewEncoder(w)
    return enc.Encode(data)
}

func getAccessToken(ctx context.Context, u *user.User) (string, error) {
    s := Setting{UserID: u.ID, Name: SettingAccessToken}
    if err := s.Load(ctx); err != nil {
        return "", err
    }
    if s.Value == "" {
        return "", ErrNoToken
    }
    return s.Value, nil
}

func setAccessToken(ctx context.Context, u *user.User, token string) error {
    s := Setting{UserID: u.ID, Name: SettingAccessToken, Value: token}
    if err := s.Save(ctx); err != nil {
        return err
    }
    return nil
}
