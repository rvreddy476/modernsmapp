import Foundation
import UsModel

public enum WebSocketState: Sendable {
    case disconnected
    case connecting
    case connected
    case error(String)
}

public protocol WebSocketClientProtocol: Sendable {
    func connect()
    func disconnect()
    func send(text: String) async throws
    func receiveStream() -> AsyncStream<String>
}

public final class WebSocketClient: WebSocketClientProtocol, @unchecked Sendable {
    private let url: URL
    private let authProvider: AuthTokenProvider?
    private var webSocketTask: URLSessionWebSocketTask?
    private var isManualDisconnect: Bool = false
    private var continuations: [UUID: AsyncStream<String>.Continuation] = [:]
    private let lock = NSLock()

    public init(url: URL, authProvider: AuthTokenProvider? = nil) {
        self.url = url
        self.authProvider = authProvider
    }

    public func connect() {
        isManualDisconnect = false
        Task {
            var request = URLRequest(url: url)
            if let token = await authProvider?.getAccessToken() {
                request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
            }

            let session = URLSession(configuration: .default)
            let task = session.webSocketTask(with: request)
            self.webSocketTask = task
            task.resume()

            self.listen()
            self.startHeartbeat()
        }
    }

    public func disconnect() {
        isManualDisconnect = true
        webSocketTask?.cancel(with: .normalClosure, reason: nil)
        webSocketTask = nil
    }

    public func send(text: String) async throws {
        guard let task = webSocketTask else {
            throw AppError.network("WebSocket is not connected")
        }
        let message = URLSessionWebSocketTask.Message.string(text)
        try await task.send(message)
    }

    public func receiveStream() -> AsyncStream<String> {
        let id = UUID()
        return AsyncStream { continuation in
            lock.lock()
            continuations[id] = continuation
            lock.unlock()

            continuation.onTermination = { [weak self] _ in
                guard let self = self else { return }
                self.lock.lock()
                self.continuations.removeValue(forKey: id)
                self.lock.unlock()
            }
        }
    }

    private func listen() {
        guard let task = webSocketTask, !isManualDisconnect else { return }

        task.receive { [weak self] result in
            guard let self = self else { return }
            switch result {
            case .success(let message):
                var text: String?
                switch message {
                case .string(let str):
                    text = str
                case .data(let data):
                    text = String(data: data, encoding: .utf8)
                @unknown default:
                    break
                }

                if let text = text {
                    self.broadcast(text)
                }
                // Continue listening
                self.listen()

            case .failure:
                if !self.isManualDisconnect {
                    // Reconnect with backoff
                    DispatchQueue.global().asyncAfter(deadline: .now() + 3.0) {
                        self.connect()
                    }
                }
            }
        }
    }

    private func broadcast(_ text: String) {
        lock.lock()
        defer { lock.unlock() }
        for continuation in continuations.values {
            continuation.yield(text)
        }
    }

    private func startHeartbeat() {
        Task {
            while !isManualDisconnect && webSocketTask != nil {
                try? await Task.sleep(nanoseconds: 25_000_000_000) // 25 seconds
                guard !isManualDisconnect else { break }
                webSocketTask?.sendPing { error in
                    if error != nil {
                        // Ping failure handled by receive loop
                    }
                }
            }
        }
    }
}
