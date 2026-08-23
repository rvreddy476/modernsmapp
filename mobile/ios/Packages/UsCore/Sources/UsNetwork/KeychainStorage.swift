import Foundation

#if canImport(Security)
import Security
#endif

public protocol KeyValueStorage: Sendable {
    func string(forKey key: String) -> String?
    @discardableResult func set(_ value: String?, forKey key: String) -> Bool
    @discardableResult func remove(forKey key: String) -> Bool
}

public final class KeychainStorage: KeyValueStorage, @unchecked Sendable {
    private let service: String
    #if !canImport(Security)
    private var memoryStorage: [String: String] = [:]
    private let lock = NSLock()
    #endif

    public init(service: String = "com.us.social.app") {
        self.service = service
    }

    public func string(forKey key: String) -> String? {
        #if canImport(Security)
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]

        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        guard status == errSecSuccess, let data = item as? Data else {
            return nil
        }
        return String(data: data, encoding: .utf8)
        #else
        lock.lock()
        defer { lock.unlock() }
        return memoryStorage[key]
        #endif
    }

    @discardableResult
    public func set(_ value: String?, forKey key: String) -> Bool {
        guard let value = value, let data = value.data(using: .utf8) else {
            return remove(forKey: key)
        }

        #if canImport(Security)
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key
        ]

        let attributes: [String: Any] = [
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        ]

        let updateStatus = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
        if updateStatus == errSecSuccess {
            return true
        }

        if updateStatus == errSecItemNotFound {
            var newQuery = query
            newQuery[kSecValueData as String] = data
            newQuery[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
            let addStatus = SecItemAdd(newQuery as CFDictionary, nil)
            return addStatus == errSecSuccess
        }

        return false
        #else
        lock.lock()
        defer { lock.unlock() }
        memoryStorage[key] = value
        return true
        #endif
    }

    @discardableResult
    public func remove(forKey key: String) -> Bool {
        #if canImport(Security)
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key
        ]
        let status = SecItemDelete(query as CFDictionary)
        return status == errSecSuccess || status == errSecItemNotFound
        #else
        lock.lock()
        defer { lock.unlock() }
        memoryStorage.removeValue(forKey: key)
        return true
        #endif
    }
}
