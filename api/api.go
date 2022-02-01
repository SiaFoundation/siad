package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
)

// A Client communicates with a Sia API server.
type Client struct {
	BaseURL      string
	AuthPassword string
}

func (c *Client) req(method string, route string, data, resp interface{}) error {
	var body io.Reader
	if data != nil {
		js, _ := json.Marshal(data)
		body = bytes.NewReader(js)
	}
	req, err := http.NewRequest(method, fmt.Sprintf("%v%v", c.BaseURL, route), body)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("", c.AuthPassword)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer io.Copy(ioutil.Discard, r.Body)
	defer r.Body.Close()
	if r.StatusCode != 200 {
		err, _ := ioutil.ReadAll(r.Body)
		return errors.New(string(err))
	}
	if resp == nil {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(resp)
}

// Get performs a GET request to the API endpoint.
func (c *Client) Get(route string, r interface{}) error { return c.req("GET", route, nil, r) }

// Post performs a POST request to the API endpoint.
func (c *Client) Post(route string, d, r interface{}) error { return c.req("POST", route, d, r) }

// Put performs a PUT request to the API endpoint.
func (c *Client) Put(route string, d interface{}) error { return c.req("PUT", route, d, nil) }

// Delete performs a DELETE request to the API endpoint.
func (c *Client) Delete(route string) error { return c.req("DELETE", route, nil, nil) }

// WriteJSON writes the JSON encoded object to the http response.
func WriteJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.Encode(v)
}

// AuthMiddleware wraps an http handler with required authentication.
func AuthMiddleware(handler http.Handler, requiredPass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, password, hasAuth := req.BasicAuth()
		if hasAuth && password == requiredPass {
			handler.ServeHTTP(w, req)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}
