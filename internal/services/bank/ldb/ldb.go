package ldb

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

var _ LDB = (*ldb)(nil)

type (
	Config struct {
		BaseURL        string `json:"base_url" mapstructure:"base_url"`
		AccessTokenURL string `json:"access_token_url" mapstructure:"access_token_url"`

		ClientID     string `json:"client_id" mapstructure:"client_id"`
		ClientSecret string `json:"client_secrey" mapstructure:"client_secret"`

		MerchantID string `json:"merchant_id" mapstructure:"merchant_id"`

		PromotionCode string `json:"promotion_code" mapstructure:"promotion_code"`

		PartnerID string `json:"partner_id" mapstructure:"partner_id"`
		KeyID     string `json:"key_id" mapstructure:"key_id"`
		HMacKey   string `json:"hmac_key" mapstructure:"hmac_key"`

		SwitchBackURL string `json:"switch_back_url" mapstructure:"switch_back_url"`
	}

	ldb struct {
		baseUrl            string
		accessTokenBaseURL string

		// clientID is the client id of LDB backend.
		clientID     string
		clientSecret string

		// merchantID is the merchant id of LDB backend.
		merchantID string

		promotionCode string

		// partnerID is the partner id of LDB backend.
		partnerID string
		keyID     string
		hmacKey   string

		// accessToken is used to authenticate with LDB backend.
		accessToken string

		// mu is used to lock access token.
		mu sync.Mutex

		// toggleTokenRefresher is used to notify token refresher to refresh token.
		toggleTokenRefresher chan struct{}

		// hc is the http client.
		hc *http.Client

		// switchBack URL is the url that LDB mobile will redirect to front-end after payment.
		switchBackURL string
	}
)

type LDB interface {
	GenQRCode(ctx context.Context, lq *LDBQRForm) (string, error)
	CheckTransaction(ctx context.Context, refID2, reqTxUUID string) (*Tx, error)
	FindCheckRefCode(ctx context.Context, refCode string) (*ResponseRef, error)
}

// New creates new instance of LDB client.
func New(ctx context.Context, cfg *Config) (LDB, error) {
	client := &ldb{
		baseUrl:            cfg.BaseURL,
		accessTokenBaseURL: cfg.AccessTokenURL,
		clientID:           cfg.ClientID,
		clientSecret:       cfg.ClientSecret,
		merchantID:         cfg.MerchantID,
		promotionCode:      cfg.PromotionCode,
		partnerID:          cfg.PartnerID,
		keyID:              cfg.KeyID,
		hmacKey:            cfg.HMacKey,
		switchBackURL:      cfg.SwitchBackURL,

		// make a buffered channel to avoid blocking.
		toggleTokenRefresher: make(chan struct{}, 1),

		// set http client with timeout.
		hc: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	token, err := client.connect(ctx)
	if err != nil {
		return nil, err
	}
	client.setAccessToken(token)

	go client.notifyAccessTokenExpired(ctx)

	return client, nil
}

type LDBQRForm struct {
	ExpiryTime      string
	TxCount         string
	Amount          decimal.Decimal
	Currency        string
	UUID            string
	ReferenceNumber string
	MobileNumber    string
	Memo            string
	IsDeepLink      bool

	// MerchantID is the merchant id custom for request.
	MerchantID string

	// ReqTxUUID is the request transaction uuid.
	ReqTxUUID string
}

func (l *ldb) GenQRCode(ctx context.Context, f *LDBQRForm) (string, error) {
	if f.MerchantID == "" {
		f.MerchantID = l.merchantID
	}
	q := qrFormReq{
		QrType:        "38",
		Platform:      "BROWSER", // ANDROID, IOS, BROWSER
		TerminalID:    nil,
		MerchantID:    f.MerchantID,
		PromotionCode: l.promotionCode,
		ExpiryTime:    f.ExpiryTime, // Minute
		TxCount:       f.TxCount,
		Amount:        f.Amount,
		Currency:      f.Currency,
		Reference1:    f.UUID,
		Reference2:    f.ReferenceNumber,
		Reference3:    nil,
		Description:   f.Memo,
		MobileNumber:  f.MobileNumber,
		ReqTxUUID:     f.ReqTxUUID,
		DeepLink: deepLink{
			IsDeepLink: isDeepLinkToStr(f.IsDeepLink),
			BackURL:    fmt.Sprintf(l.switchBackURL+"/%s/ticket", f.UUID), // TODO: change to env
			BackInfo:   "ewent.la",
		},
	}

	emvCode, err := l.getQRFromLDB(ctx, &q)
	if err != nil {
		return "", err
	}

	return emvCode, nil
}

func (l *ldb) CheckTransaction(ctx context.Context, refID2, reqTxUUID string) (*Tx, error) {
	return l.checkTransaction(ctx, refID2, reqTxUUID)
}

func (l *ldb) FindCheckRefCode(ctx context.Context, refCode string) (*ResponseRef, error) {
	return l.findCheckRefCode(ctx, refCode)
}
