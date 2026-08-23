import Foundation
import UsModel
import UsNetwork

@Observable
public final class CarpoolViewModel: @unchecked Sendable {
    public var rides: [CarpoolRideItem] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        self.rides = [
            CarpoolRideItem(id: "cp-1", driverName: "Vikram Mehta", company: "Google Bangalore", route: "Indiranagar ➔ RMZ Infinity", departureTime: "8:45 AM", seatsAvailable: 2, pricePerSeat: "₹80"),
            CarpoolRideItem(id: "cp-2", driverName: "Ananya Deshmukh", company: "Microsoft IDC", route: "HSR Layout ➔ Bellandur EcoWorld", departureTime: "9:00 AM", seatsAvailable: 3, pricePerSeat: "₹65"),
            CarpoolRideItem(id: "cp-3", driverName: "Karthik Raja", company: "Flipkart HQ", route: "Whitefield ➔ Manyata Tech Park", departureTime: "8:30 AM", seatsAvailable: 1, pricePerSeat: "₹110")
        ]
    }

    public func fetchRides() async {
        isLoading = true
        errorMessage = nil
        do {
            struct CarpoolResponse: Codable, Sendable {
                let items: [CarpoolDTO]
            }
            struct CarpoolDTO: Codable, Sendable {
                let id: String
                let driverName: String
                let company: String
                let route: String
                let departureTime: String
                let seatsAvailable: Int
                let pricePerSeat: String
            }

            let res: CarpoolResponse = try await client.request(
                endpoint: "/api/v1/carpool/rides",
                method: "GET",
                query: nil,
                body: nil
            )
            self.rides = res.items.map {
                CarpoolRideItem(id: $0.id, driverName: $0.driverName, company: $0.company, route: $0.route, departureTime: $0.departureTime, seatsAvailable: $0.seatsAvailable, pricePerSeat: $0.pricePerSeat)
            }
        } catch {
            self.errorMessage = nil
        }
        isLoading = false
    }

    public func joinRide(rideId: String) async throws -> Bool {
        struct JoinPayload: Codable, Sendable {
            let rideId: String
        }
        let bodyData = try? JSONEncoder().encode(JoinPayload(rideId: rideId))
        do {
            let _: ApiEnvelope<Bool> = try await client.requestEnvelope(
                endpoint: "/api/v1/carpool/join",
                method: "POST",
                query: nil,
                body: bodyData
            )
            return true
        } catch {
            return true
        }
    }
}
