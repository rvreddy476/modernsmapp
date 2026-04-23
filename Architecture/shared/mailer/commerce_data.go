package mailer

// Data structs for commerce email templates.
// All monetary fields are formatted strings (e.g. "1,299.00") to avoid template formatting gymnastics.

type OrderConfirmationData struct {
	OrderNumber    string
	OrderDate      string
	PaymentMethod  string
	Items          []OrderItemLine
	Subtotal       string
	Shipping       string
	Tax            string
	CouponCode     string
	CouponDiscount string
	Total          string
	StoreName      string
	ShipName       string
	ShipLine1      string
	ShipLine2      string
	ShipCity       string
	ShipState      string
	ShipPostal     string
	ShipPhone      string
	TrackURL       string
}

type OrderItemLine struct {
	Title     string
	Quantity  int
	UnitPrice string
	LineTotal string
}

type PaymentReceiptData struct {
	OrderNumber   string
	Amount        string
	TransactionID string
	Gateway       string
	PaidAt        string
}

type InvoiceEmailData struct {
	OrderNumber   string
	InvoiceNumber string
	InvoiceDate   string
	Total         string
	InvoiceURL    string
}

type ShipmentShippedData struct {
	OrderNumber    string
	Courier        string
	TrackingNumber string
	ETA            string
	TrackURL       string
}

type ShipmentDeliveredData struct {
	OrderNumber string
	DeliveredAt string
	ReviewURL   string
}

type SellerNewOrderData struct {
	OrderNumber  string
	Amount       string
	ItemCount    int
	NetPayout    string
	DashboardURL string
}
