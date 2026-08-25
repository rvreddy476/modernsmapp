import SwiftUI
import UsDesignSystem
import UsNetwork

@main
struct UsApp: App {
    private let client = APIClient()
    @State private var deepLinkCoordinator = DeepLinkCoordinator.shared

    var body: some Scene {
        WindowGroup {
            RootTabView(client: client)
                .preferredColorScheme(.dark)
                .onOpenURL { url in
                    _ = deepLinkCoordinator.handle(url: url)
                }
        }
    }
}
