import Foundation

public enum RideVehicleType: String, CaseIterable, Identifiable, Codable, Sendable {
    case auto = "US Auto"
    case bike = "US Moto"
    case cab = "US Go Cab"
    case premier = "US Premier"

    public var id: String { rawValue }

    public var icon: String {
        switch self {
        case .auto: return "circle.hexagongrid.fill"
        case .bike: return "bicycle"
        case .cab: return "car.fill"
        case .premier: return "car.side.fill"
        }
    }

    public var etaMins: Int {
        switch self {
        case .auto: return 3
        case .bike: return 2
        case .cab: return 5
        case .premier: return 6
        }
    }

    public var priceMultiplier: Double {
        switch self {
        case .bike: return 0.6
        case .auto: return 1.0
        case .cab: return 1.6
        case .premier: return 2.2
        }
    }
}
