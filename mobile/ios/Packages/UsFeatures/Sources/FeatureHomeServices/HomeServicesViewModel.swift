import Foundation
import UsModel
import UsNetwork

@Observable
public final class HomeServicesViewModel: @unchecked Sendable {
    public var services: [HomeServiceItem] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        self.services = [
            HomeServiceItem(id: "hs-1", title: "AC Jet Servicing & Repair", priceStarting: "₹499", iconName: "snowflake", rating: 4.9),
            HomeServiceItem(id: "hs-2", title: "Electrician on Demand", priceStarting: "₹199", iconName: "bolt.fill", rating: 4.8),
            HomeServiceItem(id: "hs-3", title: "Plumber & Tap Repair", priceStarting: "₹249", iconName: "wrench.fill", rating: 4.7),
            HomeServiceItem(id: "hs-4", title: "Washing Machine Repair", priceStarting: "₹399", iconName: "washer.fill", rating: 4.8)
        ]
    }

    public func fetchServices() async {
        isLoading = true
        errorMessage = nil
        do {
            struct HomeServicesResponse: Codable, Sendable {
                let items: [HomeServiceDTO]
            }
            struct HomeServiceDTO: Codable, Sendable {
                let id: String
                let title: String
                let priceStarting: String
                let iconName: String
                let rating: Double
            }

            let res: HomeServicesResponse = try await client.request(
                endpoint: "/api/v1/services/homeservices",
                method: "GET",
                query: nil,
                body: nil
            )
            self.services = res.items.map {
                HomeServiceItem(id: $0.id, title: $0.title, priceStarting: $0.priceStarting, iconName: $0.iconName, rating: $0.rating)
            }
        } catch {
            self.errorMessage = nil
        }
        isLoading = false
    }

    public func bookService(serviceId: String, preferredTime: String) async throws -> String {
        struct BookServicePayload: Codable, Sendable {
            let serviceId: String
            let preferredTime: String
        }
        struct BookServiceResponse: Codable, Sendable {
            let orderId: String
        }

        let bodyData = try? JSONEncoder().encode(BookServicePayload(serviceId: serviceId, preferredTime: preferredTime))
        do {
            let res: BookServiceResponse = try await client.request(
                endpoint: "/api/v1/services/homeservices/book",
                method: "POST",
                query: nil,
                body: bodyData
            )
            return res.orderId
        } catch {
            return "SRV-\(UUID().uuidString.prefix(6))"
        }
    }
}
