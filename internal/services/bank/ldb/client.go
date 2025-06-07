package ldb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"ticket-system/internal/status"
	"time"

	"github.com/shopspring/decimal"
)

const (
	GrantTypeDefaultStr = "client_credentials"
)

// notifyAccessTokenExpired do infinite loop with period of time
// to perform auto renew token from LDB backend with
// exponential backOff strategy.
func (l *ldb) notifyAccessTokenExpired(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			ticker.Stop()
			return

		case <-ticker.C:
			// log.Println("notifyAccessTokenExpired: ticker.C => token expired")

		case <-l.toggleTokenRefresher:
			log.Println("notifyAccessTokenExpired: toggleTokenRefresher => token refreshed")
		}

		// reconnect with exponential backOff strategy
		backOff := time.Second

	Retry:
		for {
			token, err := l.connect(ctx)
			switch err {
			case nil:
				l.setAccessToken(token)

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
func (l *ldb) setAccessToken(accessToken string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.accessToken = accessToken
}

// getAccessToken get access token from client.
func (l *ldb) getAccessToken() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.accessToken
}

// connect makes http call to perform authentication with LDB backend.
func (l *ldb) connect(ctx context.Context) (string, error) {
	query := url.Values{"grant_type": []string{GrantTypeDefaultStr}}
	body := strings.NewReader(query.Encode())

	// _baseURL, _ := url.Parse(l.accessTokenBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.accessTokenBaseURL, body)
	if err != nil {
		return "", fmt.Errorf("connectLDB: http.NewRequestWithContext: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.URL.User = url.UserPassword(l.clientID, l.clientSecret)

	resp, err := l.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("connectLDB: http.DefaultClient.Do: %w", err)
	}
	defer resp.Body.Close()

	// toggle token refresher if unauthorized
	if resp.StatusCode == http.StatusUnauthorized {
		l.toggleTokenRefresher <- struct{}{}
		return "", errors.New("connectLDB: resp.StatusCode: 401 => Unauthorized")
	}

	if resp.StatusCode != http.StatusOK {
		rbody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("connectLDB: resp.StatusCode: %d, resp.Body: %s", resp.StatusCode, rbody)
	}

	var reply struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&reply); err != nil {
		return "", fmt.Errorf("connectLDB: json.Decode: %w", err)
	}

	return fmt.Sprintf("%s %s", reply.TokenType, reply.AccessToken), nil
}

// qrQuery is the query to generate QR code from ldb.
type (
	qrFormReq struct {
		QrType   string `json:"qrType"`
		Platform string `json:"platformType"`

		MerchantID string  `json:"merchantId"`
		TerminalID *string `json:"terminalId"`

		PromotionCode string `json:"promotionCode"`
		ExpiryTime    string `json:"expiryTime"`
		TxCount       string `json:"makeTxnTime"`

		Amount   decimal.Decimal `json:"amount"`
		Currency string          `json:"currency"`

		Reference1 string  `json:"ref1"`
		Reference2 string  `json:"ref2"`
		Reference3 *string `json:"ref3"`

		Description  string `json:"metadata"`
		MobileNumber string `json:"mobileNum"`

		DeepLink deepLink `json:"deeplinkMetaData"`

		// ReqTxUUID is the request transaction UUID.
		ReqTxUUID string
	}

	// deepLink is the deep link to generate QR code from ldb.
	deepLink struct {
		IsDeepLink string `json:"deeplink"`
		BackURL    string `json:"switchBackURL"`
		BackInfo   string `json:"switchBackInfo"`
	}
)

// QRCode generate QR code from ldb.
func (l *ldb) getQRFromLDB(ctx context.Context, q *qrFormReq) (string, error) {
	b, err := json.Marshal(q)
	if err != nil {
		return "", fmt.Errorf("getQRFromLDB: json.Marshal: %w", err)
	}
	body := bytes.NewBuffer(b)

	_baseURL, _ := url.Parse(l.baseUrl)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s%s", _baseURL, "/vboxConsumers/api/v1/qrpayment/initiate.service"), body)
	if err != nil {
		return "", fmt.Errorf("getQRFromLDB: http.NewRequestWithContext: %w", err)
	}
	req = l.setHeaders(req, q.ReqTxUUID)

	resp, err := l.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("getQRFromLDB: http.DefaultClient.Do: %w", err)
	}
	defer resp.Body.Close()

	// toggle token refresher if unauthorized
	if resp.StatusCode == http.StatusUnauthorized {
		l.toggleTokenRefresher <- struct{}{}
		return "", errors.New("getQRFromLDB: resp.StatusCode: 401 => Unauthorized")
	}

	if resp.StatusCode != http.StatusOK {
		rbody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("getQRFromLDB: resp.StatusCode: %d, resp.Body: %s", resp.StatusCode, rbody)
	}

	var reply struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Compose struct {
			EmvCode  string `json:"qrCode"`
			DeepLink struct {
				URL string `json:"deeplinkURL"`
			} `json:"deeplinkInfo"`
		} `json:"dataResponse"`
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&reply); err != nil {
		return "", fmt.Errorf("getQRFromLDB: json.Decode: %w", err)
	}
	if reply.Status != "00" {
		return "", fmt.Errorf("getQRFromLDB: reply.Status: %v, reply.Message: %v", reply.Status, reply.Message)
	}

	return reply.Compose.EmvCode, nil
}

type (
	Tx struct {
		RefID       string
		UUID        string
		RefNumber   string
		Ccy         string
		Amount      decimal.Decimal
		PaymentBank string
		CreatedAt   time.Time
	}

	TxReply struct {
		Status       string       `json:"status"`
		Message      string       `json:"message"`
		DataResponse DataResponse `json:"dataResponse"`
	}

	DataResponse struct {
		PartnerOrderID   string            `json:"partnerOrderID"`
		PartnerPaymentID string            `json:"partnerPaymentID"`
		TxnItem          []TransactionItem `json:"txnItem"`
	}

	TransactionItem struct {
		ProcessingStatus string          `json:"processingStatus"`
		PaymentBank      string          `json:"paymentBank"`
		PaymentAt        string          `json:"paymentAt"`
		PaymentReference string          `json:"paymentReference"`
		Amount           decimal.Decimal `json:"amount"`
		Currency         string          `json:"currency"`
		PayerName        interface{}     `json:"payerName"`
		Contact          interface{}     `json:"contact"`
	}
)

// checkTransaction check transaction status from ldb.
func (l *ldb) checkTransaction(ctx context.Context, refID2, reqTxUUID string) (*Tx, error) {
	queryParams := url.Values{}
	queryParams.Set("reference2", refID2)

	_baseURL, _ := url.Parse(l.baseUrl)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/vboxConsumers/api/v1/qrpayment/%s/inquiry.service?%s", _baseURL, reqTxUUID, queryParams.Encode()), nil)
	if err != nil {
		return nil, fmt.Errorf("checkTransactionLDB http.NewRequestWithContext: %w", err)
	}
	req = l.setHeaders(req, reqTxUUID)

	resp, err := l.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("checkTransactionLDB http.DefaultClient.Do: %w", err)
	}
	defer resp.Body.Close()

	// toggle token refresher if unauthorized
	if resp.StatusCode == http.StatusUnauthorized {
		l.toggleTokenRefresher <- struct{}{}
		return nil, errors.New("checkTransactionLDB resp.StatusCode: 401 => Unauthorized")
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("checkTransactionLDB resp.StatusCode: %d, resp.Body: %s", resp.StatusCode, respBody)
	}

	var reply TxReply
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&reply); err != nil {
		return nil, fmt.Errorf("checkTransactionLDB json.Decode: %w", err)
	}

	if reply.Status != "00" {
		log.Printf("[CheckTransactionLDB] reply.Status: %v, reply.Message: %v", reply.Status, reply.Message)

		if reply.Message == "INQUIRY_TXN_EMPTY" || reply.Message == "PARTNER_ID_INCORRECT" {
			return nil, status.ErrFailedPayment
		}

		return nil, fmt.Errorf("checkTransactionLDB reply.Status: %v, reply.Message: %v", reply.Status, reply.Message)
	}

	txItem := reply.DataResponse.TxnItem[0]
	t, _ := time.ParseInLocation("2006-01-02 15:04:05", txItem.PaymentAt, time.Local)
	tx := &Tx{
		RefID:       txItem.PaymentReference,
		UUID:        reply.DataResponse.PartnerOrderID,
		RefNumber:   reply.DataResponse.PartnerPaymentID,
		Ccy:         txItem.Currency,
		Amount:      txItem.Amount,
		PaymentBank: txItem.PaymentBank,
		CreatedAt:   t,
	}

	return tx, nil
}

// dataRef is the data response from ldb.
type (
	MessageRef struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}

	DataRef struct {
		EvenCode    string `json:"evenCode"`
		BookingID   string `json:"bookingId"`
		LDBRef      string `json:"ldbRef"`
		DateBooking string `json:"dateBooking"`
	}

	ResponseRef struct {
		Message MessageRef `json:"message"`
		Data    DataRef    `json:"data"`
	}
)

// findCheckRefCode find check ref code from ldb.
func (l *ldb) findCheckRefCode(ctx context.Context, refCode string) (*ResponseRef, error) {
	b, err := json.Marshal(map[string]string{"ref": refCode})
	if err != nil {
		return nil, fmt.Errorf("findCheckRefCodeLDB json.Marshal: %w", err)
	}
	body := bytes.NewBuffer(b)

	_baseURL, _ := url.Parse("https://dehome.ldblao.la/referal/prod/api/v1/getRefLDB")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, _baseURL.String(), body)
	if err != nil {
		return nil, fmt.Errorf("findCheckRefCodeLDB http.NewRequestWithContext: %w", err)
	}
	req = l.setHeaders(req, "")

	resp, err := l.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("findCheckRefCodeLDB http.DefaultClient.Do: %w", err)
	}
	defer resp.Body.Close()

	// toggle token refresher if unauthorized
	if resp.StatusCode == http.StatusUnauthorized {
		l.toggleTokenRefresher <- struct{}{}
		return nil, errors.New("findCheckRefCodeLDB resp.StatusCode: 401 => Unauthorized")
	}

	if resp.StatusCode != http.StatusOK {
		rbody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("findCheckRefCodeLDB resp.StatusCode: %d, resp.Body: %s", resp.StatusCode, rbody)
	}

	var reply ResponseRef
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&reply); err != nil {
		return nil, fmt.Errorf("findCheckRefCodeLDB json.Decode: %w", err)
	}
	if reply.Message.Code != "00" {
		log.Printf("findCheckRefCodeLDB reply.Message.Code: %v, reply.Message.Detail: %v", reply.Message.Code, reply.Message.Detail)
		return nil, status.ErrRefCodeNotFound
	}

	return &reply, nil
}
