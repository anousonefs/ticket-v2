package jdb

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"ticket-system/internal/status"
	"time"

	pubnub "github.com/pubnub/go/v7"
	"github.com/shopspring/decimal"
)

type (
	Config struct {
		AID        string `json:"aid" mapstructure:"aid"`
		IIN        string `json:"iin" mapstructure:"iin"`
		ReceiverID string `json:"receiverId" mapstructure:"receiverId"`
		MCC        string `json:"mcc" mapstructure:"mcc"`
		CCy        string `json:"ccy" mapstructure:"ccy"`
		Country    string `json:"conntry" mapstructure:"country"`
		MName      string `json:"MName" mapstructure:"mname"`
		MCity      string `json:"mcity" mapstructure:"mcity"`

		PNSubKey    string `json:"pn_subkey" mapstructure:"pn_subkey"`
		PNSubSecret string `json:"pn_subsecret" mapstructure:"pn_subsecret"`
		PNUUID      string `json:"pn_uuid" mapstructure:"pn_uuid"`
		PNChannel   string `json:"pn_channel" mapstructure:"pn_channel"`
		PNCipherKey string `json:"pn_cipherKey" mapstructure:"pn_cipherkey"`

		BaseURL string `json:"baseUrl" mapstructure:"base_url"`

		PartnerID string `json:"partnerId" mapstructure:"partner_id"`
		ClientID  string `json:"clientId" mapstructure:"client_id"`
		ClientKey string `json:"clientKey" mapstructure:"client_key"`
		HMACKey   string `json:"hmacKey" mapstructure:"hmac_key"`
	}

	Yespay struct {
		AID        string
		IIN        string
		MerchantID string
		MCC        string
		CCy        string
		Country    string
		MName      string
		MCity      string

		pnSubKey    string
		pnSubSecret string
		pnUUID      string
		pnChannels  []string
		pnCipherKey string

		pn       *pubnub.PubNub
		listener *pubnub.Listener
		sub      *subscribe

		client *Client
	}
)

type (
	payload struct {
		RefID         string          `json:"refNo"`
		UUID          string          `json:"billNumber"`
		FCCRef        string          `json:"exReferenceNo"`
		Ccy           string          `json:"sourceCurrency"`
		Payer         string          `json:"sourceName"`
		AccountNumber string          `json:"sourceAccount"`
		Amount        decimal.Decimal `json:"txnAmount"`
		CreatedAt     string          `json:"txnDateTime"`
	}
)

// New returns a new YesPay instance.
func New(ctx context.Context, cfg *Config) (*Yespay, error) {
	client := newClient(ctx, &ClientConfig{
		BaseURL:   cfg.BaseURL,
		PartnerID: cfg.PartnerID,
		ClientID:  cfg.ClientID,
		ClientKey: cfg.ClientKey,
		HMACKey:   cfg.HMACKey,
	})

	// Connect to JDB backend. Get access token.
	token, err := client.connect(ctx)
	if err != nil {
		return nil, err
	}
	client.setAccessToken(token)

	// Notify access token expired.
	go client.notifyAccessTokenExpired(ctx)

	y := &Yespay{
		AID:        cfg.AID,
		IIN:        cfg.IIN,
		MerchantID: cfg.ReceiverID,
		MCC:        cfg.MCC,
		CCy:        cfg.CCy,
		Country:    cfg.Country,
		MName:      cfg.MName,
		MCity:      cfg.MCity,

		pnSubKey:    cfg.PNSubKey,
		pnSubSecret: cfg.PNSubSecret,
		pnUUID:      cfg.PNUUID,
		pnChannels:  []string{cfg.PNChannel},
		pnCipherKey: cfg.PNCipherKey,
		listener:    pubnub.NewListener(),

		client: client,
	}

	// Set YesPay's PubNub config.
	pnCfg := pubnub.NewConfigWithUserId(pubnub.UserId(y.pnUUID))
	pnCfg.SubscribeKey = y.pnSubKey
	pnCfg.CipherKey = y.pnCipherKey
	pnCfg.SecretKey = y.pnSubSecret

	// newSubscription Subscribe to YesPay's PubNub channel.
	newSub, err := y.newSubscription(ctx, pnCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to YesPay's PubNub channel: %v", err)
	}

	// Add listener to YesPay's PubNub.
	newSub.pn.AddListener(newSub.lis)
	y.sub = newSub

	return y, nil
}

type subscribe struct {
	pn  *pubnub.PubNub
	lis *pubnub.Listener
	ch  chan *status.Transaction
}

func (y *Yespay) newSubscription(ctx context.Context, pnCfg *pubnub.Config) (*subscribe, error) {
	sub := &subscribe{
		pn:  pubnub.NewPubNub(pnCfg),
		lis: pubnub.NewListener(),
	}

	go sub.processSubscription(ctx)

	return sub, nil
}

func (s *subscribe) processSubscription(ctx context.Context) error {
	listener := s.lis
	for {
		select {
		case status := <-listener.Status:
			switch status.Category {
			case pubnub.PNConnectedCategory:
				log.Println("connected to pubnub")

			case pubnub.PNReconnectedCategory:
				log.Println("reconnected to pubnub")

			case pubnub.PNDisconnectedCategory:
				log.Println("disconnected from pubnub")

			case pubnub.PNRequestMessageCountExceededCategory:
				log.Println("request message count exceeded connect to pubnub")

			case pubnub.PNAccessDeniedCategory:
				log.Println("access denied connect to pubnub")

			case pubnub.PNAcknowledgmentCategory:
				log.Println("acknowledgment connect to pubnub")

			case pubnub.PNBadRequestCategory:
				log.Println("bad request connect to pubnub")

			case pubnub.PNNoStubMatchedCategory:
				log.Println("no stub matched connect to pubnub")

			case pubnub.PNReconnectionAttemptsExhausted:
				log.Println("reconnection attempts exhausted connect to pubnub")

			case pubnub.PNCancelledCategory:
				log.Println("cancelled connect to pubnub")

			case pubnub.PNLoopStopCategory:
				log.Println("loop stopped connect to pubnub")

			case pubnub.PNTimeoutCategory:
				log.Println("timeout connect to pubnub")

			case pubnub.PNUnknownCategory:
				log.Println("unknown status category event occurred connect to pubnub")

			default:
				log.Println("unknown status category connect to pubnub")
			}

		case message := <-listener.Message:
			log.Println("message received pubnub: ", message.Message)

			var p payload
			dec := json.NewDecoder(strings.NewReader(message.Message.(string)))
			if err := dec.Decode(&p); err != nil {
				log.Println(err)
				continue
			}

			tran, err := p.ToDomain()
			if err != nil {
				log.Println(err)
				continue
			}
			s.ch <- tran

		case <-ctx.Done():
			log.Println("close subscribe")
			return nil
		}
	}
}

func (p *payload) ToDomain() (*status.Transaction, error) {
	ts, err := time.ParseInLocation("2006-01-02 15:04:05", p.CreatedAt, time.Local)
	if err != nil {
		return nil, err
	}

	return &status.Transaction{
		RefID:         p.RefID,
		UUID:          p.UUID,
		FCCRef:        p.FCCRef,
		Ccy:           p.Ccy,
		Payer:         p.Payer,
		AccountNumber: p.AccountNumber,
		Amount:        p.Amount,
		CreatedAt:     ts,
	}, nil
}

func (y *Yespay) addChannel(_ context.Context, uuid string) {
	// Add channel name to YesPay's PubNub.
	channel := fmt.Sprintf("%s_%s", y.MerchantID, uuid)

	// Get last 2 minutes timetoken.
	tt := time.Now().Add(time.Duration(-2*time.Minute)).Unix() * 10000

	// Subscribe to YesPay's PubNub channel.
	y.sub.pn.Subscribe().Channels([]string{channel}).Timetoken(tt).Execute()
}

func (y *Yespay) Unsubscribe(ctx context.Context, uuid string) {
	y.sub.pn.Unsubscribe().Channels([]string{fmt.Sprintf("%s_%s", y.MerchantID, uuid)}).Execute()
}

func (y *Yespay) SetTranChannel(ch chan *status.Transaction) {
	y.sub.ch = ch
}

func (y *Yespay) CheckTransaction(ctx context.Context, uuid string) (*status.Transaction, error) {
	return y.client.checkTransaction(ctx, uuid)
}

func (y *Yespay) GenQRCode(ctx context.Context, f *status.FormQR) (string, error) {
	if f.MerchantID == "" {
		f.MerchantID = y.MerchantID
	}
	emvCode, err := y.client.getQRFromJDB(ctx, f)
	if err != nil {
		return "", err
	}

	y.addChannel(ctx, f.UUID)

	return emvCode, nil
}
