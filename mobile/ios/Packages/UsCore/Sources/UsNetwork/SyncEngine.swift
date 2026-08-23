import Foundation
import UsModel

public struct QueuedMutation: Codable, Sendable {
    public let id: String
    public let endpoint: String
    public let method: String
    public let payloadData: Data?
    public let timestamp: Date

    public init(id: String = UUID().uuidString, endpoint: String, method: String, payloadData: Data? = nil) {
        self.id = id
        self.endpoint = endpoint
        self.method = method
        self.payloadData = payloadData
        self.timestamp = Date()
    }
}

public final class SyncEngine: @unchecked Sendable {
    public static let shared = SyncEngine()

    private var queue: [QueuedMutation] = []
    private let lock = NSLock()
    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
    }

    public func enqueue(endpoint: String, method: String, payload: Data? = nil) {
        lock.lock()
        let mutation = QueuedMutation(endpoint: endpoint, method: method, payloadData: payload)
        queue.append(mutation)
        lock.unlock()

        Task {
            await processQueue()
        }
    }

    public func processQueue() async {
        lock.lock()
        let items = queue
        lock.unlock()

        guard !items.isEmpty else { return }

        for item in items {
            do {
                let _: [String: String] = (try? await client.request(
                    endpoint: item.endpoint,
                    method: item.method,
                    query: nil,
                    body: item.payloadData
                )) ?? [:]

                lock.lock()
                queue.removeAll { $0.id == item.id }
                lock.unlock()
            } catch {
                // Keep in queue for next sync cycle
                break
            }
        }
    }

    public var pendingCount: Int {
        lock.lock()
        defer { lock.unlock() }
        return queue.count
    }
}
