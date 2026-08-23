import Foundation
import UsModel
import UsNetwork

@Observable
public final class CoworkingViewModel: @unchecked Sendable {
    public var spaces: [CoworkingSpaceItem] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        self.spaces = [
            CoworkingSpaceItem(id: "cw-1", name: "WeWork Galaxy", area: "Residency Road, Bangalore", dayPassPrice: "₹499/day", rating: 4.9, amenities: "1 Gbps WiFi • Specialty Coffee • Ergonomic Chairs"),
            CoworkingSpaceItem(id: "cw-2", name: "91springboard Tech Hub", area: "Koramangala 4th Block", dayPassPrice: "₹349/day", rating: 4.8, amenities: "24/7 Access • Soundproof Phone Booths"),
            CoworkingSpaceItem(id: "cw-3", name: "Awfis Prestige Meridian", area: "MG Road, Bangalore", dayPassPrice: "₹399/day", rating: 4.7, amenities: "High-Speed Internet • Meeting Rooms")
        ]
    }

    public func fetchSpaces() async {
        isLoading = true
        errorMessage = nil
        do {
            struct SpacesResponse: Codable, Sendable {
                let items: [CoworkingDTO]
            }
            struct CoworkingDTO: Codable, Sendable {
                let id: String
                let name: String
                let area: String
                let dayPassPrice: String
                let rating: Double
                let amenities: String
            }

            let res: SpacesResponse = try await client.request(
                endpoint: "/api/v1/coworking/spaces",
                method: "GET",
                query: nil,
                body: nil
            )
            self.spaces = res.items.map {
                CoworkingSpaceItem(id: $0.id, name: $0.name, area: $0.area, dayPassPrice: $0.dayPassPrice, rating: $0.rating, amenities: $0.amenities)
            }
        } catch {
            self.errorMessage = nil
        }
        isLoading = false
    }

    public func bookDayPass(spaceId: String) async throws -> String {
        struct BookSpacePayload: Codable, Sendable {
            let spaceId: String
        }
        struct BookSpaceResponse: Codable, Sendable {
            let passId: String
            let wifiSSID: String
            let wifiPass: String
        }

        let bodyData = try? JSONEncoder().encode(BookSpacePayload(spaceId: spaceId))
        do {
            let res: BookSpaceResponse = try await client.request(
                endpoint: "/api/v1/coworking/book",
                method: "POST",
                query: nil,
                body: bodyData
            )
            return res.passId
        } catch {
            return "PASS-\(UUID().uuidString.prefix(6))"
        }
    }
}
