package main

import (
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "sync"

    "github.com/bradberger/goinstagram"

    "golang.org/x/net/context"

    "google.golang.org/appengine"
    "google.golang.org/appengine/datastore"
    "google.golang.org/appengine/log"
    "google.golang.org/appengine/urlfetch"
    "google.golang.org/appengine/user"
)

func getHostname(ctx context.Context) string {
    if appengine.IsDevAppServer() {
        return "http://localhost:8080"
    }
    return fmt.Sprintf("https://%s", appengine.DefaultVersionHostname(ctx))
}

func getClient(ctx context.Context, accessToken string) *instagram.Instagram {
    client := instagram.NewClient(func(config *instagram.Config) {
        config.ClientId = os.Getenv("INSTAGRAM_CLIENT_ID")
        config.ClientSecret = os.Getenv("INSTAGRAM_CLIENT_SECRET")
        config.RedirectUri = fmt.Sprintf("%s/api/v1/authorize/callback", getHostname(ctx))
    })
    client.Client = urlfetch.Client(ctx)
    if accessToken != "" {
        client.SetAccessToken(accessToken)
    }
    return client
}

func getClientByUser(ctx context.Context, u *user.User) (*instagram.Instagram, error) {
    token, err := getAccessToken(ctx, u)
    if err != nil {
        return nil, err
    }
    return getClient(ctx, token), nil
}

func getAccessTokenByUserID(ctx context.Context, userID string) (string, error) {
    s := Setting{Name: SettingAccessToken, UserID: userID}
    if err := s.Load(ctx); err != nil {
        return "", err
    }
    return s.Value, nil
}

func followRequestHandler(w http.ResponseWriter, r *http.Request) {

    ctx := appengine.NewContext(r)
    u, err := checkUser(w, r, ctx)
    if err != nil {
        code := http.StatusInternalServerError
        http.Error(w, http.StatusText(code), code)
        return
    }

    client, err := getClientByUser(ctx, u)
    if err != nil {
        log.Warningf(ctx, "Could not get Instagram client for user %+v: %v", u, err)
        code := http.StatusBadRequest
        http.Error(w, http.StatusText(code), code)
        return
    }

    items, _, err := client.Relationships.RequestedBy()
    if err != nil {
        log.Warningf(ctx, "Could not get Instagram client for requestedBy for %+v: %v", u, err)
        code := http.StatusBadRequest
        http.Error(w, http.StatusText(code), code)
        return
    }

    writeJSON(w, items)
}

func accessTokenCheckHandler(w http.ResponseWriter, r *http.Request) {

    ctx := appengine.NewContext(r)
    u, err := checkUser(w, r, ctx)
    if err != nil {
        return
    }

    s := Setting{UserID: u.ID, Name: SettingAccessToken}
    if err := s.Load(ctx); err != nil {
        log.Debugf(ctx, "Could not load access token: %v", err)
    }

    if err := s.Write(w); err != nil {
        log.Errorf(ctx, "Error writing access token: %v", err)
        code := http.StatusInternalServerError
        http.Error(w, http.StatusText(code), code)
        return
    }

    return
}

func authorizeBeginHandler(w http.ResponseWriter, r *http.Request) {
    ctx := appengine.NewContext(r)
    client := getClient(ctx, "")
    _, err := checkUser(w, r, ctx)
    if err != nil {
        return
    }

    url := client.AuthorizeURLWithScope([]string{"relationships","follower_list"})
    http.Redirect(w, r, url, http.StatusTemporaryRedirect)
    return
}

func authorizeCallbackHandler(w http.ResponseWriter, r *http.Request) {

    ctx := appengine.NewContext(r)
    code := r.FormValue("code")
    u, err := checkUser(w, r, ctx)
    if err != nil {
        return
    }

    client := getClient(ctx, "")
    auth, err := client.RequestAccessToken(urlfetch.Client(ctx), code)
    if err != nil {
        log.Errorf(ctx, "Error getting access token: %v", err)
        code := http.StatusInternalServerError
        http.Error(w, http.StatusText(code), code)
        return
    }

    log.Debugf(ctx, "Got access token: %+v", auth)

    s := Setting{UserID: u.ID, Name: SettingAccessToken, Value: auth.AccessToken}
    if err := s.Save(ctx); err != nil {
        log.Errorf(ctx, "Could not save access token: %v", err)
        code := http.StatusInternalServerError
        http.Error(w, http.StatusText(code), code)
        return
    }

    http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
    return
}

func enableHandler(w http.ResponseWriter, r *http.Request) {

    ctx := appengine.NewContext(r)
    u, err := checkUser(w, r, ctx)
    if err != nil {
        return
    }

    // Won't be able to load if not saved/if deleted
    s := Setting{UserID: u.ID, Name: SettingAutofollowEnabled, Value: ""}
    if err := s.Load(ctx); err != nil {
        log.Debugf(ctx, "Could not get setting: %v (%+v)", err, s)
    }

    switch r.Method {
    case http.MethodGet:
        if err := s.Write(w); err != nil {
            internalServerError(w, ctx, "Could encode setting to JSON: %v (%+v)", err, s)
            return
        }
        return
    case http.MethodPut:
        fallthrough
    case http.MethodPost:
        s.Value = "true"
        if err := s.Save(ctx); err != nil {
            internalServerError(w, ctx, "Could not put datastore item: %v (%+v)", err, s)
            return
        }
        if err := s.Write(w); err != nil {
            internalServerError(w, ctx, "Could encode setting to JSON: %v (%+v)", err, s)
            return
        }
        return
    case http.MethodDelete:
        if err := s.Delete(ctx); err != nil {
            internalServerError(w, ctx, "Could not delete datastore item: %v (%+v)", err, s)
            return
        }
        w.WriteHeader(http.StatusNoContent)
        return
    }

}

func userHandler(w http.ResponseWriter, r *http.Request) {

    enc := json.NewEncoder(w)
    ctx := appengine.NewContext(r)
    u, err := checkUser(w, r, ctx)
    if err != nil {
        return
    }

    url, _ := user.LogoutURL(ctx, "/")
    w.Header().Set("Logout-URL", url)

    if err := enc.Encode(u); err != nil {
        code := http.StatusInternalServerError
        http.Error(w, http.StatusText(code), code)
        return
    }

    return
}

func autofollowCronHandler(w http.ResponseWriter, r *http.Request) {

    var wg sync.WaitGroup
    var settings []Setting

    ctx := appengine.NewContext(r)
    q := datastore.NewQuery(EntitySettings).Filter("Name =", SettingAutofollowEnabled)
    if userID := r.FormValue("user"); userID != "" {
        log.Debugf(ctx, "Running autofollowCronHandler for user %s only", userID)
        q.Filter("UserID =", userID)
    }

    if _, err := q.GetAll(ctx, &settings); err != nil {
        log.Errorf(ctx, "Error getting users to check: %v", err)
    }

    for _, s := range settings {
        wg.Add(1)
        go func(s Setting) {
            defer wg.Done()
            token, err := getAccessTokenByUserID(ctx, s.UserID)
            if err != nil {
                log.Errorf(ctx, "Couldn't get access token for %s: %v", s.UserID, err)
                return
            }

            client := getClient(ctx, token)
            items, _, err := client.Relationships.RequestedBy()
            if err != nil {
                log.Errorf(ctx, "Couldn't get follow requests for %s: %v", s.UserID, err)
                return
            }

            for _, req := range items {
                wg.Add(1)
                go func(req instagram.User) {
                    defer wg.Done()
                    log.Debugf(ctx, "Approving follow from %s for %s", req.Id, s.UserID)
                    _, _, err := client.Relationships.PostRelationship(req.Id, "approve")
                    if err != nil {
                        log.Debugf(ctx, "Couldn't approve %s: %v", req.Id, err)
                    } else {
                        log.Infof(ctx, "Approved %s", req.Id)
                    }
                }(req)
            }
        }(s)
    }
    wg.Wait()

    return
}
