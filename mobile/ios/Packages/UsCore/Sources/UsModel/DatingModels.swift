import Foundation

public struct DatingProfile: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let name: String
    public let age: Int
    public let bio: String
    public let occupation: String
    public let distanceKm: Int
    public let photos: [String]
    public let interests: [String]

    public init(
        id: String,
        name: String,
        age: Int,
        bio: String,
        occupation: String = "Designer & Creator",
        distanceKm: Int = 4,
        photos: [String] = [],
        interests: [String] = ["Photography", "Coffee", "Travel", "Music"]
    ) {
        self.id = id
        self.name = name
        self.age = age
        self.bio = bio
        self.occupation = occupation
        self.distanceKm = distanceKm
        self.photos = photos
        self.interests = interests
    }
}
