package mailer

// Commerce email templates. All templates use html/template syntax.
// Variables expected are declared above each constant as struct types in commerce_data.go.

const OrderConfirmationTemplate = `<!DOCTYPE html>
<html><head><title>Order #{{.OrderNumber}} confirmed — {{.StoreName}}</title>
<meta charset="utf-8"></head>
<body style="font-family:-apple-system,BlinkMacSystemFont,sans-serif;background:#f5f5f5;margin:0;padding:24px;color:#111">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:12px;overflow:hidden;box-shadow:0 1px 3px rgba(0,0,0,0.08)">
  <div style="background:linear-gradient(135deg,#6366f1,#8b5cf6);padding:32px 24px;color:#fff">
    <h1 style="margin:0;font-size:22px;font-weight:800">Order confirmed ✓</h1>
    <p style="margin:8px 0 0;opacity:0.9">We've received your order. Thanks for shopping with Postbook.</p>
  </div>
  <div style="padding:24px">
    <table style="width:100%;font-size:14px">
      <tr><td style="color:#666">Order Number</td><td style="text-align:right;font-weight:600">#{{.OrderNumber}}</td></tr>
      <tr><td style="color:#666">Order Date</td><td style="text-align:right">{{.OrderDate}}</td></tr>
      <tr><td style="color:#666">Payment</td><td style="text-align:right">{{.PaymentMethod}}</td></tr>
    </table>
    <hr style="border:none;border-top:1px solid #eee;margin:20px 0"/>
    <h3 style="margin:0 0 12px;font-size:15px">Items</h3>
    {{range .Items}}
    <div style="display:flex;justify-content:space-between;padding:8px 0;border-bottom:1px solid #f3f3f3">
      <div><b>{{.Title}}</b><br/><small style="color:#888">Qty {{.Quantity}} × ₹{{.UnitPrice}}</small></div>
      <div style="font-weight:600">₹{{.LineTotal}}</div>
    </div>
    {{end}}
    <table style="width:100%;margin-top:20px;font-size:14px">
      <tr><td style="color:#666">Subtotal</td><td style="text-align:right">₹{{.Subtotal}}</td></tr>
      <tr><td style="color:#666">Shipping</td><td style="text-align:right">₹{{.Shipping}}</td></tr>
      <tr><td style="color:#666">Tax (GST)</td><td style="text-align:right">₹{{.Tax}}</td></tr>
      {{if .CouponDiscount}}<tr><td style="color:#666">Coupon ({{.CouponCode}})</td><td style="text-align:right;color:#16a34a">−₹{{.CouponDiscount}}</td></tr>{{end}}
      <tr><td style="font-weight:700;font-size:16px;padding-top:8px">Total</td><td style="text-align:right;font-weight:700;font-size:16px;padding-top:8px">₹{{.Total}}</td></tr>
    </table>
    <hr style="border:none;border-top:1px solid #eee;margin:20px 0"/>
    <h3 style="margin:0 0 8px;font-size:15px">Shipping to</h3>
    <p style="color:#555;line-height:1.6;margin:0">{{.ShipName}}<br/>{{.ShipLine1}}{{if .ShipLine2}}, {{.ShipLine2}}{{end}}<br/>{{.ShipCity}}, {{.ShipState}} {{.ShipPostal}}<br/>{{.ShipPhone}}</p>
    <div style="margin-top:24px;text-align:center">
      <a href="{{.TrackURL}}" style="display:inline-block;padding:12px 24px;background:#6366f1;color:#fff;text-decoration:none;border-radius:8px;font-weight:600">Track your order</a>
    </div>
  </div>
  <div style="background:#fafafa;padding:16px 24px;text-align:center;color:#888;font-size:12px">
    Need help? Reply to this email or visit postbook.app/help
  </div>
</div>
</body></html>`

const PaymentReceiptTemplate = `<!DOCTYPE html>
<html><head><title>Payment received for order #{{.OrderNumber}}</title><meta charset="utf-8"></head>
<body style="font-family:-apple-system,sans-serif;background:#f5f5f5;margin:0;padding:24px;color:#111">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
  <h1 style="color:#16a34a;margin:0 0 8px">Payment received ✓</h1>
  <p style="color:#555">Your payment of <b>₹{{.Amount}}</b> for order <b>#{{.OrderNumber}}</b> has been received successfully.</p>
  <table style="width:100%;margin-top:20px;font-size:14px">
    <tr><td style="color:#666">Transaction ID</td><td style="text-align:right;font-family:monospace">{{.TransactionID}}</td></tr>
    <tr><td style="color:#666">Gateway</td><td style="text-align:right">{{.Gateway}}</td></tr>
    <tr><td style="color:#666">Paid At</td><td style="text-align:right">{{.PaidAt}}</td></tr>
  </table>
  <p style="margin-top:24px;color:#666;font-size:13px">An invoice will be sent separately once your order is dispatched.</p>
</div></body></html>`

const InvoiceEmailTemplate = `<!DOCTYPE html>
<html><head><title>Invoice for order #{{.OrderNumber}}</title><meta charset="utf-8"></head>
<body style="font-family:-apple-system,sans-serif;background:#f5f5f5;margin:0;padding:24px;color:#111">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
  <h1 style="margin:0 0 8px">Invoice attached</h1>
  <p style="color:#555">Your tax invoice for order <b>#{{.OrderNumber}}</b> is attached to this email.</p>
  <table style="width:100%;margin-top:20px;font-size:14px">
    <tr><td style="color:#666">Invoice Number</td><td style="text-align:right;font-weight:600">{{.InvoiceNumber}}</td></tr>
    <tr><td style="color:#666">Invoice Date</td><td style="text-align:right">{{.InvoiceDate}}</td></tr>
    <tr><td style="color:#666">Total Amount</td><td style="text-align:right;font-weight:700">₹{{.Total}}</td></tr>
  </table>
  {{if .InvoiceURL}}
  <div style="margin-top:24px;text-align:center">
    <a href="{{.InvoiceURL}}" style="display:inline-block;padding:12px 24px;background:#6366f1;color:#fff;text-decoration:none;border-radius:8px;font-weight:600">View Invoice Online</a>
  </div>
  {{end}}
</div></body></html>`

const ShipmentShippedTemplate = `<!DOCTYPE html>
<html><head><title>Order #{{.OrderNumber}} shipped 📦</title><meta charset="utf-8"></head>
<body style="font-family:-apple-system,sans-serif;background:#f5f5f5;margin:0;padding:24px;color:#111">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
  <div style="font-size:48px;text-align:center">📦</div>
  <h1 style="margin:8px 0;text-align:center">Your order is on the way!</h1>
  <p style="color:#555;text-align:center">Order <b>#{{.OrderNumber}}</b> has been shipped via <b>{{.Courier}}</b>.</p>
  <table style="width:100%;margin-top:20px;font-size:14px">
    <tr><td style="color:#666">Tracking Number</td><td style="text-align:right;font-family:monospace;font-weight:600">{{.TrackingNumber}}</td></tr>
    <tr><td style="color:#666">Carrier</td><td style="text-align:right">{{.Courier}}</td></tr>
    <tr><td style="color:#666">Estimated Delivery</td><td style="text-align:right">{{.ETA}}</td></tr>
  </table>
  <div style="margin-top:24px;text-align:center">
    <a href="{{.TrackURL}}" style="display:inline-block;padding:12px 24px;background:#6366f1;color:#fff;text-decoration:none;border-radius:8px;font-weight:600">Track package →</a>
  </div>
</div></body></html>`

const ShipmentDeliveredTemplate = `<!DOCTYPE html>
<html><head><title>Order #{{.OrderNumber}} delivered ✓</title><meta charset="utf-8"></head>
<body style="font-family:-apple-system,sans-serif;background:#f5f5f5;margin:0;padding:24px;color:#111">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
  <div style="font-size:48px;text-align:center">✅</div>
  <h1 style="margin:8px 0;text-align:center">Delivered!</h1>
  <p style="color:#555;text-align:center">Order <b>#{{.OrderNumber}}</b> was delivered on {{.DeliveredAt}}.</p>
  <p style="color:#555;text-align:center;margin-top:24px">How was it? Your review helps other shoppers.</p>
  <div style="text-align:center;margin-top:16px">
    <a href="{{.ReviewURL}}" style="display:inline-block;padding:12px 24px;background:#f59e0b;color:#fff;text-decoration:none;border-radius:8px;font-weight:600">Write a review ★</a>
  </div>
</div></body></html>`

const SellerNewOrderTemplate = `<!DOCTYPE html>
<html><head><title>New order received · #{{.OrderNumber}}</title><meta charset="utf-8"></head>
<body style="font-family:-apple-system,sans-serif;background:#f5f5f5;margin:0;padding:24px;color:#111">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
  <h1 style="margin:0 0 8px;color:#16a34a">💰 New order!</h1>
  <p style="color:#555">You've received a new order worth <b>₹{{.Amount}}</b>.</p>
  <table style="width:100%;margin-top:20px;font-size:14px">
    <tr><td style="color:#666">Order Number</td><td style="text-align:right;font-weight:600">#{{.OrderNumber}}</td></tr>
    <tr><td style="color:#666">Items</td><td style="text-align:right">{{.ItemCount}}</td></tr>
    <tr><td style="color:#666">Net Payout</td><td style="text-align:right;font-weight:700;color:#16a34a">₹{{.NetPayout}}</td></tr>
  </table>
  <p style="margin-top:20px;color:#666;font-size:13px">Please pack and ship within <b>48 hours</b> to maintain your seller rating.</p>
  <div style="margin-top:24px;text-align:center">
    <a href="{{.DashboardURL}}" style="display:inline-block;padding:12px 24px;background:#6366f1;color:#fff;text-decoration:none;border-radius:8px;font-weight:600">Open seller dashboard</a>
  </div>
</div></body></html>`
