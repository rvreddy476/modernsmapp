import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct HomeServiceItem: Identifiable {
    public let id: String
    public let title: String
    public let priceStarting: String
    public let iconName: String
    public let rating: Double

    public init(id: String, title: String, priceStarting: String, iconName: String, rating: Double = 4.8) {
        self.id = id
        self.title = title
        self.priceStarting = priceStarting
        self.iconName = iconName
        self.rating = rating
    }
}

public struct HomeServicesView: View {
    @State private var viewModel: HomeServicesViewModel
    public let onDismiss: () -> Void

    public init(
        client: APIClientProtocol = APIClient(),
        onDismiss: @escaping () -> Void = {}
    ) {
        _viewModel = State(initialValue: HomeServicesViewModel(client: client))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // Trust Badge Banner
                        HStack(spacing: 12) {
                            ZStack {
                                Circle().fill(UsColors.postbookPrimary.opacity(0.2)).frame(width: 44, height: 44)
                                Image(systemName: "shield.checkmark.fill")
                                    .foregroundColor(UsColors.postbookPrimary)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Verified Home Professionals 🛠️")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("30-Day service warranty • Background verified")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Popular Home Services")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        if viewModel.isLoading {
                            ProgressView()
                                .tint(UsColors.postbookPrimary)
                                .frame(maxWidth: .infinity, alignment: .center)
                                .padding(.vertical, 20)
                        } else {
                            LazyVStack(spacing: 12) {
                                ForEach(viewModel.services) { svc in
                                    serviceCard(svc)
                                }
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Home Services")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .task {
                await viewModel.fetchServices()
            }
        }
    }

    @ViewBuilder
    private func serviceCard(_ svc: HomeServiceItem) -> some View {
        HStack(spacing: 14) {
            ZStack {
                RoundedRectangle(cornerRadius: 12)
                    .fill(UsColors.bgTertiary)
                    .frame(width: 48, height: 48)

                Image(systemName: svc.iconName)
                    .font(.system(size: 22))
                    .foregroundColor(UsColors.postbookPrimary)
            }

            VStack(alignment: .leading, spacing: 2) {
                Text(svc.title)
                    .font(.system(size: 14, weight: .bold))
                    .foregroundColor(UsColors.textPrimary)

                HStack(spacing: 4) {
                    Image(systemName: "star.fill")
                        .font(.system(size: 10))
                        .foregroundColor(.yellow)
                    Text(String(format: "%.1f", svc.rating))
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                    Text("• Starts at \(svc.priceStarting)")
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)
                }
            }

            Spacer()

            Button(action: {
                Task {
                    let orderId = try? await viewModel.bookService(serviceId: svc.id, preferredTime: "Tomorrow, 10:00 AM")
                    HapticManager.shared.trigger(.success)
                    ToastManager.shared.show("Booked \(svc.title)! ID: \(orderId ?? "SRV-OK")", style: .success)
                }
            }) {
                Text("Book")
                    .font(.system(size: 12, weight: .bold))
                    .foregroundColor(.black)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)
                    .background(Color.white)
                    .clipShape(Capsule())
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
    }
}
