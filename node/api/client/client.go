package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/node/api"

	"gitlab.com/NebulousLabs/errors"
)

type (
	// A Client makes requests to the siad HTTP API.
	Client struct {
		Options
	}

	// Options defines the options that are available when creating a
	// client.
	Options struct {
		// Address is the API address of the siad server.
		Address string

		// Password must match the password of the siad server.
		Password string

		// UserAgent must match the User-Agent required by the siad server. If not
		// set, it defaults to "Sia-Agent".
		UserAgent string

		// CheckRedirect is an optional handler to be called if the request
		// receives a redirect status code.
		// For more see https://golang.org/pkg/net/http/#Client
		CheckRedirect func(req *http.Request, via []*http.Request) error
	}

	// A UnsafeClient is a Client with additional access to unsafe methods that
	// are easy to misuse. It should only be used for testing.
	UnsafeClient struct {
		Client
	}
)

// NewUnsafeClient creates a new UnsafeClient using the provided address.
func NewUnsafeClient(client Client) *UnsafeClient {
	return &UnsafeClient{client}
}

// Post makes a POST request to the resource at `resource`, using `data` as the
// request body. The response, if provided, will be decoded into `obj`.
func (uc *UnsafeClient) Post(resource string, data string, obj interface{}) error {
	return uc.post(resource, data, obj)
}

// Get requests the specified resource. The response, if provided, will be
// decoded into obj. The resource path must begin with /.
func (uc *UnsafeClient) Get(resource string, obj interface{}) error {
	return uc.get(resource, obj)
}

// GetWithHeaders requests the specified resource using the given
// request headers.
func (uc *UnsafeClient) GetWithHeaders(resource string, headers http.Header) (*http.Response, error) {
	req, err := uc.NewRequest("GET", resource, nil)
	if err != nil {
		return nil, errors.AddContext(err, "failed to construct GET request")
	}

	// Decorate the headers on the request object
	for k, v := range headers {
		for _, vv := range v {
			req.Header.Add(k, vv)
		}
	}

	httpClient := http.Client{CheckRedirect: uc.CheckRedirect}
	return httpClient.Do(req)
}

// New creates a new Client using the provided address. The password will be set
// using build.APIPassword and the user agent will be set to "Sia-Agent". Both
// can be changed manually by the caller after the client is returned.
func New(opts Options) *Client {
	return &Client{
		Options: opts,
	}
}

// DefaultOptions returns the default options for a client. This includes
// setting the default siad user agent to "Sia-Agent" and setting the password
// using the build.APIPassword() function.
func DefaultOptions() (Options, error) {
	pw, err := build.APIPassword()
	if err != nil {
		return Options{}, errors.AddContext(err, "could not locate api password")
	}
	return Options{
		Address:   "localhost:9980",
		Password:  pw,
		UserAgent: "Sia-Agent",
	}, nil
}

// NewRequest constructs a request to the siad HTTP API, setting the correct
// User-Agent and Basic Auth. The resource path must begin with /.
func (c *Client) NewRequest(method, resource string, body io.Reader) (*http.Request, error) {
	url := "http://" + c.Address + resource
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	agent := c.UserAgent
	if agent == "" {
		agent = "Sia-Agent"
	}
	req.Header.Set("User-Agent", agent)
	if c.Password != "" {
		req.SetBasicAuth("", c.Password)
	}
	return req, nil
}

// drainAndClose reads rc until EOF and then closes it. drainAndClose should
// always be called on HTTP response bodies, because if the body is not fully
// read, the underlying connection can't be reused.
func drainAndClose(rc io.ReadCloser) {
	io.Copy(ioutil.Discard, rc)
	rc.Close()
}

// readAPIError decodes and returns an api.Error.
func readAPIError(r io.Reader) error {
	var apiErr api.Error
	b, _ := ioutil.ReadAll(r)
	if err := json.NewDecoder(bytes.NewReader(b)).Decode(&apiErr); err != nil {
		fmt.Println("raw resp", string(b))
		return errors.AddContext(err, "could not read error response")
	}

	if strings.Contains(apiErr.Error(), ErrPeerExists.Error()) {
		return ErrPeerExists
	}

	return apiErr
}

// getRawResponse requests the specified resource. The response, if provided,
// will be returned in a byte slice
func (c *Client) getRawResponse(resource string) (http.Header, []byte, error) {
	header, reader, err := c.getReaderResponse(resource)
	if err != nil {
		return nil, nil, errors.AddContext(err, "failed to get reader response")
	}
	// Possible to get a nil reader if there is no response.
	if reader == nil {
		return header, nil, nil
	}
	defer drainAndClose(reader)
	d, err := ioutil.ReadAll(reader)
	return header, d, errors.AddContext(err, "failed to read all bytes from reader")
}

// getReaderResponse requests the specified resource. The response, if provided,
// will be returned as an io.Reader.
func (c *Client) getReaderResponse(resource string) (http.Header, io.ReadCloser, error) {
	req, err := c.NewRequest("GET", resource, nil)
	if err != nil {
		return nil, nil, errors.AddContext(err, "failed to construct GET request")
	}
	httpClient := http.Client{CheckRedirect: c.CheckRedirect}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, errors.AddContext(err, "GET request failed")
	}

	// Add ErrAPICallNotRecognized if StatusCode is StatusModuleNotLoaded to
	// allow for handling of modules that are not loaded
	if res.StatusCode == api.StatusModuleNotLoaded || res.StatusCode == api.StatusModuleDisabled {
		err = errors.Compose(readAPIError(res.Body), api.ErrAPICallNotRecognized)
		return nil, nil, errors.AddContext(err, "unable to perform GET on "+resource)
	}

	// If the status code is not 2xx, decode and return the accompanying
	// api.Error.
	if res.StatusCode < 200 || res.StatusCode > 299 {
		err := readAPIError(res.Body)
		drainAndClose(res.Body)
		return nil, nil, errors.AddContext(err, "GET request error")
	}

	if res.StatusCode == http.StatusNoContent {
		// no reason to read the response
		drainAndClose(res.Body)
		return res.Header, nil, nil
	}
	return res.Header, res.Body, nil
}

// getRawResponse requests part of the specified resource. The response, if
// provided, will be returned in a byte slice
func (c *Client) getRawPartialResponse(resource string, from, to uint64) ([]byte, error) {
	req, err := c.NewRequest("GET", resource, nil)
	if err != nil {
		return nil, errors.AddContext(err, "failed to construct GET request")
	}
	req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", from, to-1))

	httpClient := http.Client{CheckRedirect: c.CheckRedirect}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, errors.AddContext(err, "GET request failed")
	}
	defer drainAndClose(res.Body)

	// Add ErrAPICallNotRecognized if StatusCode is StatusModuleNotLoaded to allow for
	// handling of modules that are not loaded
	if res.StatusCode == api.StatusModuleNotLoaded || res.StatusCode == api.StatusModuleDisabled {
		err = errors.Compose(readAPIError(res.Body), api.ErrAPICallNotRecognized)
		return nil, errors.AddContext(err, "unable to perform GET on "+resource)
	}

	// If the status code is not 2xx, decode and return the accompanying
	// api.Error.
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, errors.AddContext(readAPIError(res.Body), "GET request error")
	}

	if res.StatusCode == http.StatusNoContent {
		// no reason to read the response
		return []byte{}, nil
	}
	return ioutil.ReadAll(res.Body)
}

// get requests the specified resource. The response, if provided, will be
// decoded into obj. The resource path must begin with /.
func (c *Client) get(resource string, obj interface{}) error {
	// Request resource
	_, data, err := c.getRawResponse(resource)
	if err != nil {
		return err
	}
	if obj == nil {
		// No need to decode response
		return nil
	}

	// Decode response
	buf := bytes.NewBuffer(data)
	err = json.NewDecoder(buf).Decode(obj)
	if err != nil {
		return errors.AddContext(err, "could not read response")
	}
	return nil
}

// head makes a HEAD request to the resource at `resource`. The headers that are
// returned are the headers that would be returned if requesting the same
// `resource` using a GET request.
func (c *Client) head(resource string) (int, http.Header, error) {
	req, err := c.NewRequest("HEAD", resource, nil)
	if err != nil {
		return 0, nil, errors.AddContext(err, "failed to construct HEAD request")
	}
	httpClient := http.Client{CheckRedirect: c.CheckRedirect}
	res, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, errors.AddContext(err, "HEAD request failed")
	}
	return res.StatusCode, res.Header, nil
}

// postRawResponse requests the specified resource. The response, if provided,
// will be returned in a byte slice
func (c *Client) postRawResponse(resource string, body io.Reader) (http.Header, []byte, error) {
	// Default the Content-Type header to "application/x-www-form-urlencoded",
	// if the caller is performing a multipart form-data upload he can do so by
	// using `postRawResponseWithHeaders` and manually set the Content-Type
	// header himself.
	headers := http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}}
	return c.postRawResponseWithHeaders(resource, body, headers)
}

// postRawResponseWithHeaders requests the specified resource and allows to pass
// custom headers. The response, if provided, will be returned in a byte slice
func (c *Client) postRawResponseWithHeaders(resource string, body io.Reader, headers http.Header) (http.Header, []byte, error) {
	req, err := c.NewRequest("POST", resource, body)
	if err != nil {
		return http.Header{}, nil, errors.AddContext(err, "failed to construct POST request")
	}

	// Decorate the headers on the request object
	for k, v := range headers {
		for _, vv := range v {
			req.Header.Add(k, vv)
		}
	}

	httpClient := http.Client{CheckRedirect: c.CheckRedirect}
	res, err := httpClient.Do(req)
	if err != nil {
		return http.Header{}, nil, errors.AddContext(err, "POST request failed")
	}
	defer drainAndClose(res.Body)

	// Add ErrAPICallNotRecognized if StatusCode is StatusModuleNotLoaded to allow for
	// handling of modules that are not loaded
	if res.StatusCode == api.StatusModuleNotLoaded || res.StatusCode == api.StatusModuleDisabled {
		err = errors.Compose(readAPIError(res.Body), api.ErrAPICallNotRecognized)
		return http.Header{}, nil, errors.AddContext(err, "unable to perform POST on "+resource)
	}

	// If the status code is not 2xx, decode and return the accompanying
	// api.Error.
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return http.Header{}, nil, errors.AddContext(readAPIError(res.Body), "POST request error")
	}

	if res.StatusCode == http.StatusNoContent {
		// no reason to read the response
		return res.Header, []byte{}, nil
	}
	d, err := ioutil.ReadAll(res.Body)
	return res.Header, d, err
}

// post makes a POST request to the resource at `resource`, using `data` as the
// request body. The response, if provided, will be decoded into `obj`.
func (c *Client) post(resource string, data string, obj interface{}) error {
	// Request resource
	_, body, err := c.postRawResponse(resource, strings.NewReader(data))
	if err != nil {
		return err
	}
	if obj == nil {
		// No need to decode response
		return nil
	}

	// Decode response
	buf := bytes.NewBuffer(body)
	err = json.NewDecoder(buf).Decode(obj)
	if err != nil {
		return errors.AddContext(err, "could not read response")
	}
	return nil
}
