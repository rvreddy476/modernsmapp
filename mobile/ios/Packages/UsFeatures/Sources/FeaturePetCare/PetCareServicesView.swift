import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct PetServiceOption: Identifiable {
    public let id: String
    public let title: String
    public let subtitle: String
    public let price: String
    public let iconName: String

    public init(id: String, title: String, subtitle: String, price: String, iconName: String) {
        self.id = id
        self.title = title
        self.subtitle = subtitle
        self.price = price
        self.iconName = iconName
    }
}

public struct PetCareServicesView: View {
    @State private var viewModel: PetCareViewModel
    public let onDismiss: () -> Void

    public init(
        client: APIClientProtocol = APIClient(),
        onDismiss: @escaping () -> Void = {}
    ) {
        _viewModel = State(initialValue: PetCareViewModel(client: client))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // Banner
                        HStack(spacing: 12) {
                            ZStack {
                                Circle().fill(UsColors.onlineGreen.opacity(0.2)).frame(width: 44, height: 44)
                                Image(systemName: "pawprint.fill")
                                    .foregroundColor(UsColors.onlineGreen)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Pet Care & Vet on Demand 🐾")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Loving certified care for dogs & cats")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Select Pet Service")
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
                                    petServiceCard(svc)
                                }
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Pet Care")
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
    private func petServiceCard(_ svc: PetServiceOption) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(svc.title)
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)
                    Text(svc.subtitle)
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                Text(svc.price)
                    .font(.system(size: 16, weight: .black, design: .rounded))
                    .foregroundColor(UsColors.onlineGreen)
            }

            Divider().background(UsColors.borderSubtle)

            Button(action: {
                Task {
                    let aptId = try? await viewModel.bookPetService(serviceId: svc.id)
                    HapticManager.shared.trigger(.success)
                    ToastManager.shared.show("Booked \(svc.title)! ID: \(aptId ?? "PET-OK") 🐾", style: .success)
                }
            }) {
                HStack {
                    Spacer()
                    Text("Book for \(svc.price)")
                        .font(.system(size: 13, weight: .bold))
                        .foregroundColor(.black)
                    Spacer()
                }
                .padding(.vertical, 10)
                .background(Color.white)
                .clipShape(RoundedRectangle(cornerRadius: 10))
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
    }
}
