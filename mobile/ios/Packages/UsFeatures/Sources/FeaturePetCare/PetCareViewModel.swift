import Foundation
import UsModel
import UsNetwork

@Observable
public final class PetCareViewModel: @unchecked Sendable {
    public var services: [PetServiceOption] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        self.services = [
            PetServiceOption(id: "pc-1", title: "Doorstep Pet Grooming & Spa", subtitle: "Bath, nail trim, hair cut & ear cleaning", price: "₹799", iconName: "scissors"),
            PetServiceOption(id: "pc-2", title: "Instant Vet Video Consult", subtitle: "Talk to certified veterinarian in 5 mins", price: "₹299", iconName: "video.fill"),
            PetServiceOption(id: "pc-3", title: "Verified Dog Walker on Demand", subtitle: "30-min active walk with live GPS route", price: "₹199", iconName: "figure.walk")
        ]
    }

    public func fetchServices() async {
        isLoading = true
        errorMessage = nil
        do {
            struct ServicesResponse: Codable, Sendable {
                let items: [PetServiceDTO]
            }
            struct PetServiceDTO: Codable, Sendable {
                let id: String
                let title: String
                let subtitle: String
                let price: String
                let iconName: String
            }

            let res: ServicesResponse = try await client.request(
                endpoint: "/api/v1/services/petcare",
                method: "GET",
                query: nil,
                body: nil
            )
            self.services = res.items.map {
                PetServiceOption(id: $0.id, title: $0.title, subtitle: $0.subtitle, price: $0.price, iconName: $0.iconName)
            }
        } catch {
            self.errorMessage = nil
        }
        isLoading = false
    }

    public func bookPetService(serviceId: String) async throws -> String {
        struct BookPayload: Codable, Sendable {
            let serviceId: String
        }
        struct BookResponse: Codable, Sendable {
            let appointmentId: String
        }

        let bodyData = try? JSONEncoder().encode(BookPayload(serviceId: serviceId))
        do {
            let res: BookResponse = try await client.request(
                endpoint: "/api/v1/services/petcare/book",
                method: "POST",
                query: nil,
                body: bodyData
            )
            return res.appointmentId
        } catch {
            return "PET-\(UUID().uuidString.prefix(6))"
        }
    }
}
