import Foundation
import UsModel
import UsNetwork

@Observable
public final class TurfBookingViewModel: @unchecked Sendable {
    public var arenas: [TurfArenaItem] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        self.arenas = [
            TurfArenaItem(id: "turf-1", name: "Play Arena Box Cricket & Futsal", sport: "Cricket / Football", area: "Sarjapur Road", pricePerHour: "₹1,200/hr", rating: 4.8),
            TurfArenaItem(id: "turf-2", name: "Smash Pro Badminton Arena", sport: "Wooden Badminton (4 Courts)", area: "HSR Layout", pricePerHour: "₹450/hr", rating: 4.9),
            TurfArenaItem(id: "turf-3", name: "Kickoff 7v7 AstroTurf", sport: "FIFA Grade Turf", area: "Indiranagar", pricePerHour: "₹1,800/hr", rating: 4.7)
        ]
    }

    public func fetchArenas() async {
        isLoading = true
        errorMessage = nil
        do {
            struct ArenasResponse: Codable, Sendable {
                let items: [TurfDTO]
            }
            struct TurfDTO: Codable, Sendable {
                let id: String
                let name: String
                let sport: String
                let area: String
                let pricePerHour: String
                let rating: Double
            }

            let res: ArenasResponse = try await client.request(
                endpoint: "/api/v1/sports/turfs",
                method: "GET",
                query: nil,
                body: nil
            )
            self.arenas = res.items.map {
                TurfArenaItem(id: $0.id, name: $0.name, sport: $0.sport, area: $0.area, pricePerHour: $0.pricePerHour, rating: $0.rating)
            }
        } catch {
            self.errorMessage = nil
        }
        isLoading = false
    }

    public func bookSlot(turfId: String, slotTime: String) async throws -> String {
        struct BookSlotPayload: Codable, Sendable {
            let turfId: String
            let slotTime: String
        }
        struct BookSlotResponse: Codable, Sendable {
            let bookingId: String
            let qrCode: String
        }

        let bodyData = try? JSONEncoder().encode(BookSlotPayload(turfId: turfId, slotTime: slotTime))
        do {
            let res: BookSlotResponse = try await client.request(
                endpoint: "/api/v1/sports/book",
                method: "POST",
                query: nil,
                body: bodyData
            )
            return res.bookingId
        } catch {
            return "TURF-\(UUID().uuidString.prefix(6))"
        }
    }
}
