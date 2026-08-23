import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public enum SuperAppService: String, CaseIterable, Identifiable {
    case wallet = "Wallet"
    case shop = "Shop"
    case dating = "Dating"
    case watch = "Watch"
    case food = "Food"
    case rides = "Rides"
    case bills = "Bill Pay"
    case live = "Live Streams"

    public var id: String { rawValue }

    public var icon: String {
        switch self {
        case .wallet: return "creditcard.fill"
        case .shop: return "bag.fill"
        case .dating: return "heart.circle.fill"
        case .watch: return "play.tv.fill"
        case .food: return "fork.knife"
        case .rides: return "car.fill"
        case .bills: return "doc.text.fill"
        case .live: return "dot.radiowaves.left.and.right"
        }
    }

    public var color: Color {
        switch self {
        case .wallet: return UsColors.postbookPrimary
        case .shop: return UsColors.posttubePrimary
        case .dating: return UsColors.postgramPrimary
        case .watch: return UsColors.postgramSecondary
        case .food: return Color.orange
        case .rides: return UsColors.onlineGreen
        case .bills: return Color.indigo
        case .live: return UsColors.liveRed
        }
    }
}

public struct ServicesHubView: View {
    public let onSelectService: (SuperAppService) -> Void

    public init(onSelectService: @escaping (SuperAppService) -> Void = { _ in }) {
        self.onSelectService = onSelectService
    }

    private let columns = [
        GridItem(.flexible(), spacing: 14),
        GridItem(.flexible(), spacing: 14),
        GridItem(.flexible(), spacing: 14),
        GridItem(.flexible(), spacing: 14)
    ]

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 24) {
                        // Wallet Quick Widget
                        walletMiniWidget

                        // Services Grid
                        VStack(alignment: .leading, spacing: 14) {
                            Text("Services & Mini-Apps")
                                .font(.system(size: 17, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            LazyVGrid(columns: columns, spacing: 18) {
                                ForEach(SuperAppService.allCases) { service in
                                    serviceGridItem(service)
                                }
                            }
                            .padding(16)
                            .background(UsColors.bgSecondary)
                            .clipShape(RoundedRectangle(cornerRadius: 16))
                        }

                        // Super-App Featured Promo Banner
                        promoBanner
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Services")
        }
    }

    private var walletMiniWidget: some View {
        Button(action: { onSelectService(.wallet) }) {
            HStack {
                VStack(alignment: .leading, spacing: 4) {
                    Text("US Pay & Wallet")
                        .font(.system(size: 12))
                        .foregroundColor(.white.opacity(0.8))
                    Text("₹4,250.00")
                        .font(.system(size: 24, weight: .bold, design: .rounded))
                        .foregroundColor(.white)
                }

                Spacer()

                HStack(spacing: 6) {
                    Text("Scan & Pay")
                        .font(.system(size: 13, weight: .bold))
                        .foregroundColor(.black)
                    Image(systemName: "qrcode.viewfinder")
                        .foregroundColor(.black)
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 8)
                .background(Color.white)
                .clipShape(Capsule())
            }
            .padding(18)
            .background(
                LinearGradient(
                    colors: [Color(red: 0x1E/255.0, green: 0x3C/255.0, blue: 0x72/255.0),
                             Color(red: 0x2A/255.0, green: 0x52/255.0, blue: 0x98/255.0)],
                    startPoint: .topLeading,
                    endPoint: .bottomTrailing
                )
            )
            .clipShape(RoundedRectangle(cornerRadius: 18))
        }
        .buttonStyle(.plain)
    }

    @ViewBuilder
    private func serviceGridItem(_ service: SuperAppService) -> some View {
        Button(action: { onSelectService(service) }) {
            VStack(spacing: 8) {
                ZStack {
                    RoundedRectangle(cornerRadius: 14)
                        .fill(service.color.opacity(0.15))
                        .frame(width: 56, height: 56)

                    Image(systemName: service.icon)
                        .font(.system(size: 24))
                        .foregroundColor(service.color)
                }

                Text(service.rawValue)
                    .font(.system(size: 12, weight: .medium))
                    .foregroundColor(UsColors.textPrimary)
                    .lineLimit(1)
            }
        }
        .buttonStyle(.plain)
    }

    private var promoBanner: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("India-First Super OS")
                .font(.system(size: 18, weight: .bold))
                .foregroundColor(.white)
            Text("From social creators to everyday UPI payments, shopping, and instant dating — all within one identity.")
                .font(.system(size: 13))
                .foregroundColor(.white.opacity(0.85))
                .lineSpacing(2)
        }
        .padding(20)
        .background(
            LinearGradient(
                colors: [UsColors.postgramPrimary, UsColors.postgramSecondary],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 18))
    }
}
