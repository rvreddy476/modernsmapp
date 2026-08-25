import Foundation

public struct WalletBalance: Codable, Sendable {
    public let currency: String
    public let balancePaise: Int64
    public let formattedBalance: String
    public let upiId: String

    public init(
        currency: String = "INR",
        balancePaise: Int64 = 425000,
        formattedBalance: String = "₹4,250.00",
        upiId: String = "alex@us"
    ) {
        self.currency = currency
        self.balancePaise = balancePaise
        self.formattedBalance = formattedBalance
        self.upiId = upiId
    }
}

public enum TransactionType: String, Codable, Sendable {
    case send
    case receive
    case topup
    case merchant
    case refund
}

public struct TransactionItem: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let type: TransactionType
    public let title: String
    public let counterparty: String
    public let amountPaise: Int64
    public let formattedAmount: String
    public let isCredit: Bool
    public let timestamp: String
    public let status: String // "SUCCESS", "PENDING", "FAILED"

    public init(
        id: String,
        type: TransactionType,
        title: String,
        counterparty: String,
        amountPaise: Int64,
        formattedAmount: String,
        isCredit: Bool,
        timestamp: String,
        status: String = "SUCCESS"
    ) {
        self.id = id
        self.type = type
        self.title = title
        self.counterparty = counterparty
        self.amountPaise = amountPaise
        self.formattedAmount = formattedAmount
        self.isCredit = isCredit
        self.timestamp = timestamp
        self.status = status
    }
}
