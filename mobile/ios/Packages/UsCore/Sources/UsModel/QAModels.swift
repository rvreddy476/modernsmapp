import Foundation

public struct AnswerItem: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let author: Author
    public let text: String
    public let upvotes: Int
    public let isVerifiedExpert: Bool
    public let createdAt: String

    public init(
        id: String,
        author: Author,
        text: String,
        upvotes: Int = 14,
        isVerifiedExpert: Bool = false,
        createdAt: String = "2h"
    ) {
        self.id = id
        self.author = author
        self.text = text
        self.upvotes = upvotes
        self.isVerifiedExpert = isVerifiedExpert
        self.createdAt = createdAt
    }
}

public struct QuestionItem: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let title: String
    public let author: Author
    public let topic: String
    public let upvotesCount: Int
    public let answersCount: Int
    public let topAnswer: AnswerItem?
    public let createdAt: String

    public init(
        id: String,
        title: String,
        author: Author,
        topic: String = "Tech",
        upvotesCount: Int = 42,
        answersCount: Int = 5,
        topAnswer: AnswerItem? = nil,
        createdAt: String = "3h"
    ) {
        self.id = id
        self.title = title
        self.author = author
        self.topic = topic
        self.upvotesCount = upvotesCount
        self.answersCount = answersCount
        self.topAnswer = topAnswer
        self.createdAt = createdAt
    }
}
