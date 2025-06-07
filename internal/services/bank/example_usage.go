package bank

import (
	"context"
	"fmt"
	"ticket-system/internal/services"
	"ticket-system/internal/services/bank/jdb"
	"ticket-system/internal/services/bank/ldb"

	"github.com/shopspring/decimal"
)

// Example demonstrates how to use the multi-bank payment interface
func ExampleUsage(ctx context.Context) error {
	// 1. Create bank factory
	factory := NewFactory()

	// 2. Create bank registry
	registry := NewBankRegistry(factory)

	// 3. Configure and register JDB bank
	jdbConfig := &jdb.Config{
		AID:        "your-aid",
		IIN:        "your-iin",
		ReceiverID: "your-receiver-id",
		// ... other JDB config fields
	}
	
	if err := registry.RegisterBank(ctx, services.BankJDB, jdbConfig); err != nil {
		return fmt.Errorf("failed to register JDB bank: %w", err)
	}

	// 4. Configure and register LDB bank
	ldbConfig := &ldb.Config{
		BaseURL:        "https://api.ldb.com",
		AccessTokenURL: "https://auth.ldb.com",
		ClientID:       "your-client-id",
		ClientSecret:   "your-client-secret",
		MerchantID:     "your-merchant-id",
		// ... other LDB config fields
	}
	
	if err := registry.RegisterBank(ctx, services.BankLDB, ldbConfig); err != nil {
		return fmt.Errorf("failed to register LDB bank: %w", err)
	}

	// 5. Set primary bank (optional - first registered bank becomes primary by default)
	if err := registry.SetPrimaryBank(services.BankJDB); err != nil {
		return fmt.Errorf("failed to set primary bank: %w", err)
	}

	// 6. Create payment service with multi-bank support
	// paymentService := services.NewPaymentServiceWithBanks(redisClient, pubnub, queueService, registry, seatService)

	// 7. Generate QR code using specific bank
	paymentReq := &services.PaymentRequest{
		Amount:          decimal.NewFromFloat(100000), // 100,000 LAK
		Currency:        "LAK",
		UUID:            "payment-123",
		ReferenceNumber: "ref-456",
		Phone:           "8562012345678",
	}

	// Generate QR using JDB
	jdbBank, err := registry.GetBank(services.BankJDB)
	if err != nil {
		return err
	}
	
	qrCodeJDB, err := jdbBank.GenerateQR(ctx, paymentReq)
	if err != nil {
		return fmt.Errorf("failed to generate JDB QR: %w", err)
	}
	fmt.Printf("JDB QR Code: %s\n", qrCodeJDB)

	// Generate QR using LDB
	ldbBank, err := registry.GetBank(services.BankLDB)
	if err != nil {
		return err
	}
	
	qrCodeLDB, err := ldbBank.GenerateQR(ctx, paymentReq)
	if err != nil {
		return fmt.Errorf("failed to generate LDB QR: %w", err)
	}
	fmt.Printf("LDB QR Code: %s\n", qrCodeLDB)

	// 8. Check transaction status
	status, err := jdbBank.CheckTransaction(ctx, "payment-123")
	if err != nil {
		return fmt.Errorf("failed to check transaction: %w", err)
	}
	fmt.Printf("Transaction Status: %+v\n", status)

	// 9. Get available banks
	availableBanks := registry.GetAvailableBanks()
	fmt.Printf("Available banks: %v\n", availableBanks)

	// 10. Cleanup
	if err := registry.Close(ctx); err != nil {
		return fmt.Errorf("failed to close bank connections: %w", err)
	}

	return nil
}

// ExampleWithPaymentService shows how to use the payment service with multi-bank support
func ExampleWithPaymentService(ctx context.Context) {
	// This example shows how you would integrate with the existing payment service

	/*
	// 1. Create bank registry (as shown above)
	registry := createBankRegistry(ctx)
	
	// 2. Create payment service
	paymentService := services.NewPaymentServiceWithBanks(
		redisClient, 
		pubnubClient, 
		queueService, 
		registry, 
		seatService,
	)

	// 3. Generate QR using payment service (will use primary bank by default)
	qrReq := services.GenerateQRRequest{
		PaymentID: "payment-123",
		BookID:    "book-456", 
		Phone:     "8562012345678",
		Amount:    decimal.NewFromFloat(100000),
		Currency:  "LAK",
		// BankProvider: services.BankJDB, // Optional: specify bank, otherwise uses primary
	}

	qrCode, err := paymentService.GenerateQRWithBank(ctx, qrReq)
	if err != nil {
		log.Printf("Failed to generate QR: %v", err)
		return
	}

	// 4. Get available banks
	availableBanks := paymentService.GetAvailableBanks()
	log.Printf("Available banks: %v", availableBanks)

	// 5. Switch primary bank
	err = paymentService.SetPrimaryBank(services.BankLDB)
	if err != nil {
		log.Printf("Failed to set primary bank: %v", err)
	}

	// 6. Check transaction with specific bank
	status, err := paymentService.CheckTransactionWithBank(ctx, services.BankJDB, "payment-123")
	if err != nil {
		log.Printf("Failed to check transaction: %v", err)
		return
	}
	log.Printf("Transaction status: %+v", status)
	*/
}