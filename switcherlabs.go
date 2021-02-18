package switcherlabs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	apiURL = "https://api.switcherlabs.com"

	// clientVersion is the SDK version
	clientVersion = "1.0.0"

	// defaultHTTPTimeout is the default timeout on the http.Client used by the SDK.
	defaultHTTPTimeout = 60 * time.Second

	typeBoolean string = "boolean"
	typeNumber  string = "number"
	typeString  string = "string"

	stateRefreshRate    = 60 * time.Second
	identityRefreshRate = 5 * time.Second
)

var (
	ErrFlagNotFound    = errors.New("flag requested does not exist")
	ErrInvalidFlagType = errors.New("flag requested is of invalid type")

	userAgent = fmt.Sprintf("switcherlabs-go/%s go/%s", clientVersion, runtime.Version())

	// The default HTTP client used for communication with SwitcherLabs API.
	//
	// If you want to use a custom http.Client you can pass one via Options.HTTPClient to
	// the NewClient function.
	httpClient = &http.Client{
		Timeout: defaultHTTPTimeout,
	}

	boolOperations = map[string]func(a, b bool) bool{
		"==": func(a, b bool) bool { return a == b },
		"!=": func(a, b bool) bool { return a != b },
	}

	numberOperations = map[string]func(a, b float64) bool{
		"==": func(a, b float64) bool { return a == b },
		"!=": func(a, b float64) bool { return a != b },
		"<":  func(a, b float64) bool { return a < b },
		"<=": func(a, b float64) bool { return a <= b },
		">":  func(a, b float64) bool { return a > b },
		">=": func(a, b float64) bool { return a >= b },
	}

	stringOperations = map[string]func(a, b string) bool{
		"==": func(a, b string) bool { return a == b },
		"!=": func(a, b string) bool { return a != b },
		"<":  func(a, b string) bool { return a < b },
		"<=": func(a, b string) bool { return a <= b },
		">":  func(a, b string) bool { return a > b },
		">=": func(a, b string) bool { return a >= b },
	}
)

// Options is used to configure a new Switcherlabs client.
type Options struct {
	// HTTPClient is an HTTP client instance to use when making API requests.
	//
	// If left unset, it'll be set to a default HTTP client for the package.
	HTTPClient *http.Client

	// URL is the base URL to use for API paths.
	//
	// This value is a pointer to allow us to differentiate an unset versus
	// empty value. Use switcherlabs.String for an easy way to set this value.
	//
	// If left empty, it'll be set to the default for the Client.
	URL *string

	// APIKey is the Switcherlabs API Key used to authenticate with API.
	APIKey string
}

type FlagOptions struct {
	Key        string
	Identifier string
}

type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	StatusCode int    `json:"status_code"`
}

// Error serializes the error object to JSON and returns it as a string.
func (e *Error) Error() string {
	ret, _ := json.Marshal(e)
	return string(ret)
}

// NewClient creates a new client to interact with Switcherlabs.
func NewClient(opts *Options) *client {
	if opts.HTTPClient == nil {
		opts.HTTPClient = httpClient
	}

	if opts.URL == nil {
		opts.URL = String(apiURL)
	}

	c := &client{
		HTTPClient: opts.HTTPClient,
		URL:        *opts.URL,
		APIKey:     opts.APIKey,

		flags:      make(map[string]*flag),
		overrides:  make(map[string]*override),
		identities: make(map[string]*identity),

		lastRefresh: time.Time{},
	}

	return c
}

// BoolFlag returns the value of the flag opts.Key for the identity with an
// identifier of opts.Identifier
func (s *client) BoolFlag(opts FlagOptions) (bool, error) {
	s.refreshState()

	// Check if the flag exists
	f, ok := s.flags[opts.Key]
	if !ok {
		return false, ErrFlagNotFound
	}
	// Check if the flag is of the requested value type
	if f.Type != typeBoolean {
		return false, ErrInvalidFlagType
	}

	// The order in which flags are evaluated is as follows:
	//
	// 	1. Identity Override Value
	// 	2. Environment Override Default Value
	// 	3. Global Flag Default Value
	//
	// If the value is a zero value (falsy), if it is set as an override (it
	// exists) that is the value that corresponds to the flag evaluation.
	//
	// We are performing a type assertion from interface{} to bool without
	// any explicit checks. This is OK because we are guaranteed that the value
	// of the flag is a boolean. Otherwise we would have returned an error
	// earlier in the function.

	var booleanValue bool

	if opts.Identifier != "" {
		i, err := s.fetchIdentity(opts.Identifier)
		if err != nil {
			return false, nil
		}

		if val, ok := i.Overrides[opts.Key]; ok {
			booleanValue = val.(bool)
			return booleanValue, nil
		}
	}

	if val, ok := s.overrides[opts.Key]; ok {
		booleanValue = val.Value.(bool)
		return booleanValue, nil
	}

	if len(f.DynamicRules) > 0 {
		for _, rule := range f.DynamicRules {
			expressionFlag := s.flagsByID[rule.Expression.FlagID]
			expressionFlagKey := expressionFlag.Key

			switch expressionFlag.Type {
			case typeBoolean:
				flagValue, err := s.BoolFlag(FlagOptions{
					Key:        expressionFlagKey,
					Identifier: opts.Identifier,
				})
				if err != nil {
					return false, err
				}

				ruleExpressionValue := rule.Expression.Value.(bool)

				if boolOperations[rule.Expression.Op](flagValue, ruleExpressionValue) {
					ruleValue := rule.Value.(bool)
					return ruleValue, nil
				}
			case typeNumber:
				flagValue, err := s.NumberFlag(FlagOptions{
					Key:        expressionFlagKey,
					Identifier: opts.Identifier,
				})
				if err != nil {
					return false, err
				}

				ruleExpressionValue := rule.Expression.Value.(float64)

				if numberOperations[rule.Expression.Op](flagValue, ruleExpressionValue) {
					ruleValue := rule.Value.(bool)
					return ruleValue, nil
				}
			case typeString:
				flagValue, err := s.StringFlag(FlagOptions{
					Key:        expressionFlagKey,
					Identifier: opts.Identifier,
				})
				if err != nil {
					return false, err
				}

				ruleExpressionValue := rule.Expression.Value.(string)

				if stringOperations[rule.Expression.Op](flagValue, ruleExpressionValue) {
					ruleValue := rule.Value.(bool)
					return ruleValue, nil
				}
			}
		}
	}

	booleanValue = f.Value.(bool)
	return booleanValue, nil
}

// NumberFlag returns the value of the flag opts.Key for the identity with an
// identifier of opts.Identifier
func (s *client) NumberFlag(opts FlagOptions) (float64, error) {
	s.refreshState()

	// Check if the flag exists
	f, ok := s.flags[opts.Key]
	if !ok {
		return 0, ErrFlagNotFound
	}
	// Check if the flag is of the requested value type
	if f.Type != typeNumber {
		return 0, ErrInvalidFlagType
	}

	// The order in which flags are evaluated is as follows:
	//
	// 	1. Identity Override Value
	// 	2. Environment Override Default Value
	// 	3. Global Flag Default Value
	//
	// If the value is a zero value (falsy), if it is set as an override (it
	// exists) that is the value that corresponds to the flag evaluation.
	//
	// We are performing a type assertion from interface{} to float64 without
	// any explicit checks. This is OK because we are guaranteed that the value
	// of the flag is a float64. Otherwise we would have returned an error
	// earlier in the function.

	var numberValue float64

	if opts.Identifier != "" {
		i, err := s.fetchIdentity(opts.Identifier)
		if err != nil {
			return 0, err
		}

		if val, ok := i.Overrides[opts.Key]; ok {
			numberValue = val.(float64)
			return numberValue, nil
		}
	}

	if val, ok := s.overrides[opts.Key]; ok {
		numberValue = val.Value.(float64)
		return numberValue, nil
	}

	if len(f.DynamicRules) > 0 {
		for _, rule := range f.DynamicRules {
			expressionFlag := s.flagsByID[rule.Expression.FlagID]
			expressionFlagKey := expressionFlag.Key

			switch expressionFlag.Type {
			case typeBoolean:
				flagValue, err := s.BoolFlag(FlagOptions{
					Key:        expressionFlagKey,
					Identifier: opts.Identifier,
				})
				if err != nil {
					return 0, err
				}

				ruleExpressionValue := rule.Expression.Value.(bool)

				if boolOperations[rule.Expression.Op](flagValue, ruleExpressionValue) {
					ruleValue := rule.Value.(float64)
					return ruleValue, nil
				}
			case typeNumber:
				flagValue, err := s.NumberFlag(FlagOptions{
					Key:        expressionFlagKey,
					Identifier: opts.Identifier,
				})
				if err != nil {
					return 0, err
				}

				ruleExpressionValue := rule.Expression.Value.(float64)

				if numberOperations[rule.Expression.Op](flagValue, ruleExpressionValue) {
					ruleValue := rule.Value.(float64)
					return ruleValue, nil
				}
			case typeString:
				flagValue, err := s.StringFlag(FlagOptions{
					Key:        expressionFlagKey,
					Identifier: opts.Identifier,
				})
				if err != nil {
					return 0, err
				}

				ruleExpressionValue := rule.Expression.Value.(string)

				if stringOperations[rule.Expression.Op](flagValue, ruleExpressionValue) {
					ruleValue := rule.Value.(float64)
					return ruleValue, nil
				}
			}
		}
	}

	numberValue = f.Value.(float64)
	return numberValue, nil
}

// StringFlag returns the value of the flag opts.Key for the identity with an
// identifier of opts.Identifier
func (s *client) StringFlag(opts FlagOptions) (string, error) {
	s.refreshState()

	// Check if the flag exists
	f, ok := s.flags[opts.Key]
	if !ok {
		return "", ErrFlagNotFound
	}
	// Check if the flag is of the requested value type
	if f.Type != typeString {
		return "", ErrInvalidFlagType
	}

	// The order in which flags are evaluated is as follows:
	//
	// 	1. Identity Override Value
	// 	2. Environment Override Default Value
	// 	3. Global Flag Default Value
	//
	// If the value is a zero value (falsy), if it is set as an override (it
	// exists) that is the value that corresponds to the flag evaluation.
	//
	// We are performing a type assertion from interface{} to string without
	// any explicit checks. This is OK because we are guaranteed that the value
	// of the flag is a string. Otherwise we would have returned an error
	// earlier in the function.

	var stringValue string

	if opts.Identifier != "" {
		i, err := s.fetchIdentity(opts.Identifier)
		if err != nil {
			return "", err
		}

		if val, ok := i.Overrides[opts.Key]; ok {
			stringValue = val.(string)
			return stringValue, nil
		}
	}

	if val, ok := s.overrides[opts.Key]; ok {
		stringValue = val.Value.(string)
		return stringValue, nil
	}

	if len(f.DynamicRules) > 0 {
		for _, rule := range f.DynamicRules {
			expressionFlag := s.flagsByID[rule.Expression.FlagID]
			expressionFlagKey := expressionFlag.Key

			switch expressionFlag.Type {
			case typeBoolean:
				flagValue, err := s.BoolFlag(FlagOptions{
					Key:        expressionFlagKey,
					Identifier: opts.Identifier,
				})
				if err != nil {
					return "", err
				}

				ruleExpressionValue := rule.Expression.Value.(bool)

				if boolOperations[rule.Expression.Op](flagValue, ruleExpressionValue) {
					ruleValue := rule.Value.(string)
					return ruleValue, nil
				}
			case typeNumber:
				flagValue, err := s.NumberFlag(FlagOptions{
					Key:        expressionFlagKey,
					Identifier: opts.Identifier,
				})
				if err != nil {
					return "", err
				}

				ruleExpressionValue := rule.Expression.Value.(float64)

				if numberOperations[rule.Expression.Op](flagValue, ruleExpressionValue) {
					ruleValue := rule.Value.(string)
					return ruleValue, nil
				}
			case typeString:
				flagValue, err := s.StringFlag(FlagOptions{
					Key:        expressionFlagKey,
					Identifier: opts.Identifier,
				})
				if err != nil {
					return "", err
				}

				ruleExpressionValue := rule.Expression.Value.(string)

				if stringOperations[rule.Expression.Op](flagValue, ruleExpressionValue) {
					ruleValue := rule.Value.(string)
					return ruleValue, nil
				}
			}
		}
	}

	stringValue = f.Value.(string)
	return stringValue, nil
}

type client struct {
	URL        string
	HTTPClient *http.Client
	APIKey     string

	flags      map[string]*flag
	flagsByID  map[string]*flag
	overrides  map[string]*override
	identities map[string]*identity

	lastRefresh time.Time

	mu sync.RWMutex
}

// call is the implementation for invoking requests to the SwitcherLabs API
func (s *client) call(method, path string, v interface{}) error {
	req, err := s.newRequest(method, path)
	if err != nil {
		return err
	}

	if err := s.do(req, v); err != nil {
		return err
	}

	return nil
}

// newRequest is used by call to generate an http.Request. It handles encoding
// parameters and attaches the appropriate headers.
func (s *client) newRequest(method, path string) (*http.Request, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	path = s.URL + path

	req, err := http.NewRequest(method, path, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth("", s.APIKey)
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", userAgent)

	return req, nil
}

// do is used by call to execute an API request and parse the response. It uses
// the backend's HTTP client to execute the request and unmarshals the response
// into v. It also handles unmarshaling errors returned by the API.
func (s *client) do(req *http.Request, v interface{}) error {
	var res *http.Response
	var err error
	var resBody []byte

	res, err = s.HTTPClient.Do(req)

	if err == nil {
		resBody, err = ioutil.ReadAll(res.Body)
		res.Body.Close()
	}

	if res.StatusCode >= 400 {
		err = s.responseToError(res, resBody)
	}

	if err != nil {
		return err
	}

	err = json.Unmarshal(resBody, v)

	return err
}

// responseToError converts an http response to an Error.
func (s *client) responseToError(res *http.Response, resBody []byte) error {
	var raw rawError
	if err := json.Unmarshal(resBody, &raw); err != nil {
		return err
	}

	return raw.Error
}

func (s *client) refreshState() error {
	now := time.Now()

	// State is still valid so short-circuit and return
	if s.lastRefresh.Add(stateRefreshRate).After(now) {
		return nil
	}

	type response struct {
		Flags     []*flag     `json:"flags"`
		Overrides []*override `json:"overrides"`
	}

	resp := &response{}

	err := s.call(http.MethodGet, "/sdk/initialize", resp)
	if err != nil {
		return err
	}

	flags := make(map[string]*flag, len(resp.Flags))
	flagsByID := make(map[string]*flag, len(resp.Flags))
	for _, f := range resp.Flags {
		flags[f.Key] = f
		flagsByID[f.ID] = f
	}

	overrides := make(map[string]*override, len(resp.Overrides))
	for _, o := range resp.Overrides {
		overrides[o.Key] = o
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.flags = flags
	s.flagsByID = flagsByID
	s.overrides = overrides

	for identifier, i := range s.identities {
		if i.isStale() {
			delete(s.identities, identifier)
		}
	}

	s.lastRefresh = now

	return nil
}

func (s *client) fetchIdentity(identifier string) (*identity, error) {
	s.mu.RLock()
	if i, ok := s.identities[identifier]; ok && !i.isStale() {
		s.mu.RUnlock()
		return i, nil
	}
	s.mu.RUnlock()

	path := fmt.Sprintf("sdk/identities/%s", identifier)

	newIdentity := &identity{}

	err := s.call(http.MethodGet, path, newIdentity)
	if err != nil {
		return nil, err
	}

	newIdentity.fetchedAt = time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.identities[newIdentity.Identifier] = newIdentity

	return newIdentity, nil
}

type flag struct {
	ID           string      `json:"id"`
	Key          string      `json:"key"`
	Type         string      `json:"type"`
	Value        interface{} `json:"value"`
	DynamicRules []struct {
		Expression struct {
			FlagID string      `json:"flag_id"`
			Op     string      `json:"op"`
			Value  interface{} `json:"value"`
		}
		Value interface{} `json:"value"`
	} `json:"dynamic_rules"`
}

type override struct {
	ID    string      `json:"id"`
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

type identity struct {
	ID         string                 `json:"id"`
	Identifier string                 `json:"identifier"`
	Overrides  map[string]interface{} `json:"overrides"`

	fetchedAt time.Time
}

func (i *identity) isStale() bool {
	return i.fetchedAt.Add(identityRefreshRate).Before(time.Now())
}

// rawError deserializes the outer JSON object returned in an error response
// from the API.
type rawError struct {
	Error *Error `json:"error,omitempty"`
}

func String(s string) *string {
	return &s
}
