package jdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"ticket-system/internal/status"
	"time"
)

type ClientConfig struct {
	BaseURL   string `json:"baseUrl" mapstructure:"base_url"`
	PartnerID string `json:"partnerId" mapstructure:"partner_id"`
	ClientID  string `json:"clientId" mapstructure:"client_id"`
	ClientKey string `json:"clientKey" mapstructure:"client_key"`
	HMACKey   string `json:"hmacKey" mapstructure:"hmac_key"`
}

type Client struct {
	// baseURL is the base url of JDB backend.
	baseURL string

	// partnerID is the partner id of JDB backend.
	partnerID string

	// clientID is the client id of JDB backend.
	clientID string

	// clientKey is the client key of JDB backend.
	clientKey string

	// hmacKey is the hmac key of JDB backend.
	hmacKey string

	// access Token is used to authenticate with JDB backend.
	accessToken string

	// mu is used to lock access token.
	mu sync.Mutex

	// toggleTokenRefresher is used to notify token refresher to refresh token.
	toggleTokenRefresher chan struct{}

	// hc is the http client.
	hc *http.Client
}

// NewClient creates new instance of JDB client.
func newClient(_ context.Context, c *ClientConfig) *Client {
	return &Client{
		baseURL:   c.BaseURL,
		partnerID: c.PartnerID,
		clientID:  c.ClientID,
		clientKey: c.ClientKey,
		hmacKey:   c.HMACKey,

		// make a buffered channel to avoid blocking.
		toggleTokenRefresher: make(chan struct{}, 1),

		// set http client with timeout.
		hc: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// notifyAccessTokenExpired do infinite loop with period of time
// to perform auto renew token from JDB backend with
// exponential backOff strategy.
func (c *Client) notifyAccessTokenExpired(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			ticker.Stop()
			return

		case <-ticker.C:
			// log.Println("notifyAccessTokenExpired: ticker.C => token expired")

		case <-c.toggleTokenRefresher:
			log.Println("notifyAccessTokenExpired: toggleTokenRefresher => token refreshed")
		}

		// reconnect with exponential backOff strategy
		backOff := time.Second

	Retry:
		for {
			token, err := c.connect(ctx)
			switch err {
			case nil:
				c.setAccessToken(token)

				break Retry

			default:
				log.Printf("notifyAccessTokenExpired: %v", err)
				select {
				case <-ctx.Done():
					return

				case <-time.After(backOff):
					backOff *= 2
				}
			}
		}
	}
}

// setAccessToken set access token to client.
func (c *Client) setAccessToken(accessToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accessToken = accessToken
}

// getAccessToken get access token from client.
func (c *Client) getAccessToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accessToken
}

// connect makes http call to perform authentication with JDB backend.
func (c *Client) connect(ctx context.Context) (string, error) {
	number, err := randomNumber()
	if err != nil {
		return "", fmt.Errorf("connectJDB: randomNumber: %v", err)
	}

	body := fmt.Sprintf(`{"requestId":%q,"partnerId":%q,"clientId":%q,"clientScret":"%s"}`, number, c.partnerID, c.clientID, c.clientKey)
	bodyReader := bytes.NewReader([]byte(body))

	_baseURL, _ := url.Parse(c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s%s", _baseURL.String(), `/api/pro/dynamic/autenticate`), bodyReader)
	if err != nil {
		return "", fmt.Errorf("connectJDB: http.NewReq: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("SignedHash", Hmac256([]byte(body), []byte(c.hmacKey)))

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("connectJDB: http.Do: %v", err)
	}
	defer resp.Body.Close()

	// toggle token refresher if unauthorized
	if resp.StatusCode == http.StatusUnauthorized {
		c.toggleTokenRefresher <- struct{}{}
		return "", errors.New("connectJDB: resp.StatusCode: 401 => Unauthorized")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("connectJDB: http.StatusCode: %v", err)
	}

	var reply struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    struct {
			AccessToken string `json:"accessToken"`
			TokenType   string `json:"tokenType"`
		} `json:"data"`
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&reply); err != nil {
		return "", fmt.Errorf("connectJDB: json.Decode: %v", err)
	}
	if reply.Status != "OK" {
		return "", fmt.Errorf("connectJDB: reply.Status: %v, reply.Message: %v", reply.Status, reply.Message)
	}

	accessToken := fmt.Sprintf("%s %s", reply.Data.TokenType, reply.Data.AccessToken)
	return accessToken, nil
}

// type FormQR struct {
// 	UUID           string
// 	Phone          string
// 	MerchantID     string
// 	ReferenceLabel string
// 	TerminalLabel  string
// 	Amount         decimal.Decimal
// }

// getQRCodeFromJDB get from JDB backend api
func (c *Client) getQRFromJDB(ctx context.Context, f *status.FormQR) (string, error) {
	number, err := randomNumber()
	if err != nil {
		return "", fmt.Errorf("getQRFromJDB: randomNumber: %v", err)
	}

	body := fmt.Sprintf(`{"requestId":%q,"partnerId":%q,"txnAmount":%s,"mechantId":%q,"billNumber":%q,"terminalId":%q,"terminalLabel":%q,"mobileNo":%q}`,
		number, c.partnerID, f.Amount, f.MerchantID, f.UUID, f.TerminalLabel, f.ReferenceLabel, f.Phone)
	bodyReader := bytes.NewReader([]byte(body))

	_baseURL, _ := url.Parse(c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s%s", _baseURL.String(), "/api/pro/dynamic/generateQr"), bodyReader)
	if err != nil {
		return "", fmt.Errorf("getQRFromJDB http.NewReq: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("SignedHash", Hmac256([]byte(body), []byte(c.hmacKey)))
	req.Header.Set("Authorization", c.getAccessToken())

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("getQRFromJDB: http.Do: %v", err)
	}
	defer resp.Body.Close()

	// toggle token refresher if unauthorized
	if resp.StatusCode == http.StatusUnauthorized {
		c.toggleTokenRefresher <- struct{}{}
		return "", errors.New("connectJDB: resp.StatusCode: 401 => Unauthorized")
	}

	var reply struct {
		Message string `json:"message"`
		Status  string `json:"status"`
		Data    struct {
			MerchantID string `json:"mcid"`
			EmvCode    string `json:"emv"`
		} `json:"data"`
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&reply); err != nil {
		return "", fmt.Errorf("getQRFromJDB: json.Decode: %w", err)
	}
	if reply.Status != "OK" {
		return "", fmt.Errorf("getQRFromJDB: reply.Status: %v, reply.Message: %v", reply.Status, reply.Message)
	}

	return reply.Data.EmvCode, nil
}

// checkTransaction check transaction status from JDB api
func (c *Client) checkTransaction(ctx context.Context, uuid string) (*status.Transaction, error) {
	number, err := randomNumber()
	if err != nil {
		return &status.Transaction{}, fmt.Errorf("checkTransactionJDB: randomNumber: %v", err)
	}

	body := fmt.Sprintf(`{"requestId":%q,"billNumber":%q}`, number, uuid)
	bodyReader := bytes.NewReader([]byte(body))

	_baseURL, _ := url.Parse(c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s%s", _baseURL.String(), "/api/pro/dynamic/checkTransaction"), bodyReader)
	if err != nil {
		return &status.Transaction{}, fmt.Errorf("checkTransactionJDB: http.NewReq: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("SignedHash", Hmac256([]byte(body), []byte(c.hmacKey)))
	req.Header.Set("Authorization", c.getAccessToken())

	resp, err := c.hc.Do(req)
	if err != nil {
		return &status.Transaction{}, fmt.Errorf("checkTransactionJDB: http.Do: %v", err)
	}
	defer resp.Body.Close()

	// toggle token refresher if unauthorized
	if resp.StatusCode == http.StatusUnauthorized {
		c.toggleTokenRefresher <- struct{}{}
		return nil, errors.New("connectJDB: resp.StatusCode: 401 => Unauthorized")
	}

	var reply struct {
		Message string `json:"message"`
		Status  string `json:"status"`
		Data    struct {
			payload
		} `json:"data"`
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&reply); err != nil {
		return &status.Transaction{}, fmt.Errorf("checkTransactionJDB: json.Decode: %v", err)
	}
	if reply.Status != "OK" {
		if reply.Status == "NOT_FOUND" {
			return &status.Transaction{}, errors.New("payment failed")
		}
		return &status.Transaction{}, fmt.Errorf("checkTransactionJDB: reply.Status: %v, reply.Message: %v", reply.Status, reply.Message)
	}

	transaction, err := reply.Data.payload.ToDomain()
	if err != nil {
		return &status.Transaction{}, fmt.Errorf("checkTransactionJDB: reply.Data: %v", err)
	}

	return transaction, nil
}
