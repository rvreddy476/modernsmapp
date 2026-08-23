import Foundation
import SwiftUI
import UsModel

@Observable
public final class DeepLinkCoordinator: @unchecked Sendable {
    public static let shared = DeepLinkCoordinator()

    public var pendingRoute: AppRoute?

    public init() {}

    public func handle(url: URL) -> Bool {
        // Universal links: https://app.us.com/posts/:id
        // Custom schemes: us://post/:id or us://user/:id
        let pathComponents = url.pathComponents.filter { $0 != "/" }

        if url.scheme == "us" {
            if let host = url.host {
                switch host {
                case "post":
                    if let postId = pathComponents.first {
                        pendingRoute = .postDetail(postId)
                        return true
                    }
                case "user":
                    if let userId = pathComponents.first {
                        pendingRoute = .userProfile(userId)
                        return true
                    }
                default:
                    break
                }
            }
        } else if url.host?.contains("us.com") == true {
            if pathComponents.count >= 2 {
                let section = pathComponents[0]
                let id = pathComponents[1]
                switch section {
                case "posts", "p":
                    pendingRoute = .postDetail(id)
                    return true
                case "users", "u":
                    pendingRoute = .userProfile(id)
                    return true
                default:
                    break
                }
            }
        }
        return false
    }

    public func handleNotification(userInfo: [AnyHashable: Any]) -> Bool {
        if let postId = userInfo["post_id"] as? String {
            pendingRoute = .postDetail(postId)
            return true
        }
        if let userId = userInfo["user_id"] as? String {
            pendingRoute = .userProfile(userId)
            return true
        }
        return false
    }
}
