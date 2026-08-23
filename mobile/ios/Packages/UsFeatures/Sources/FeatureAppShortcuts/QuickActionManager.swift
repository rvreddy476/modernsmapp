import SwiftUI
import UIKit

public enum AppQuickActionType: String {
    case scanUPI = "com.us.social.scanUPI"
    case createPost = "com.us.social.createPost"
    case openChat = "com.us.social.openChat"
    case explore = "com.us.social.explore"
}

@Observable
public final class QuickActionManager: @unchecked Sendable {
    public static let shared = QuickActionManager()

    public var pendingQuickAction: AppQuickActionType? = nil

    public func handleShortcutItem(_ shortcutItem: UIApplicationShortcutItem) -> Bool {
        guard let action = AppQuickActionType(rawValue: shortcutItem.type) else {
            return false
        }
        self.pendingQuickAction = action
        return true
    }
}
