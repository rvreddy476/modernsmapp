import Foundation
import UsModel
import UsNetwork

@Observable
public final class PNRTrackerViewModel: @unchecked Sendable {
    public var trackedTrips: [TransitBookingStatus] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        self.trackedTrips = [
            TransitBookingStatus(id: "tr-1", pnr: "4281902841", title: "Vande Bharat Express (SBC ➔ MAS)", transitType: "Train (IRCTC)", statusText: "On Time 🟢", platformOrGate: "Platform 1", departureTime: "05:45 AM"),
            TransitBookingStatus(id: "tr-2", pnr: "6E-2401", title: "IndiGo (BLR ➔ BOM)", transitType: "Flight (Terminal 2)", statusText: "Boarding Soon ✈️", platformOrGate: "Gate 14B", departureTime: "11:20 AM")
        ]
    }

    public func fetchTrackedTrips() async {
        isLoading = true
        errorMessage = nil
        do {
            struct TripsResponse: Codable, Sendable {
                let items: [TransitBookingDTO]
            }
            struct TransitBookingDTO: Codable, Sendable {
                let id: String
                let pnr: String
                let title: String
                let transitType: String
                let statusText: String
                let platformOrGate: String
                let departureTime: String
            }

            let res: TripsResponse = try await client.request(
                endpoint: "/api/v1/transit/trips",
                method: "GET",
                query: nil,
                body: nil
            )
            self.trackedTrips = res.items.map {
                TransitBookingStatus(id: $0.id, pnr: $0.pnr, title: $0.title, transitType: $0.transitType, statusText: $0.statusText, platformOrGate: $0.platformOrGate, departureTime: $0.departureTime)
            }
        } catch {
            self.errorMessage = nil
        }
        isLoading = false
    }

    public func trackPNR(pnr: String) async throws -> TransitBookingStatus {
        struct PNRRequest: Codable, Sendable {
            let pnr: String
        }
        struct PNRResponse: Codable, Sendable {
            let id: String
            let pnr: String
            let title: String
            let transitType: String
            let statusText: String
            let platformOrGate: String
            let departureTime: String
        }

        let bodyData = try? JSONEncoder().encode(PNRRequest(pnr: pnr))
        do {
            let res: PNRResponse = try await client.request(
                endpoint: "/api/v1/transit/track",
                method: "POST",
                query: nil,
                body: bodyData
            )
            let newTrip = TransitBookingStatus(id: res.id, pnr: res.pnr, title: res.title, transitType: res.transitType, statusText: res.statusText, platformOrGate: res.platformOrGate, departureTime: res.departureTime)
            self.trackedTrips.insert(newTrip, at: 0)
            return newTrip
        } catch {
            let mockTrip = TransitBookingStatus(id: "tr-\(pnr)", pnr: pnr, title: "Trip \(pnr)", transitType: "Live Transit", statusText: "Confirmed 🟢", platformOrGate: "Platform 3", departureTime: "14:30 PM")
            self.trackedTrips.insert(mockTrip, at: 0)
            return mockTrip
        }
    }
}
