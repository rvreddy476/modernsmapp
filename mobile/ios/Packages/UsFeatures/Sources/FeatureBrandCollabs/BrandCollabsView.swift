import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct BrandDealItem: Identifiable {
    public let id: String
    public let brandName: String
    public let campaignTitle: String
    public let payoutAmount: String
    public let deliverables: String
    public let deadline: String

    public init(id: String, brandName: String, campaignTitle: String, payoutAmount: String, deliverables: String, deadline: String) {
        self.id = id
        self.brandName = brandName
        self.campaignTitle = campaignTitle
        self.payoutAmount = payoutAmount
        self.deliverables = deliverables
        self.deadline = deadline
    }
}

public struct BrandCollabsView: View {
    public let onDismiss: () -> Void

    @State private var deals: [BrandDealItem] = [
        BrandDealItem(id: "bd-1", brandName: "Nothing Tech India", campaignTitle: "Nothing Phone (3) AI OS Showcase", payoutAmount: "₹85,000", deliverables: "1x Reel + 2x Stories", deadline: "Sep 15, 2026"),
        BrandDealItem(id: "bd-2", brandName: "Sleepy Owl Coffee", campaignTitle: "Cold Brew Can Summer Launch", payoutAmount: "₹35,000", deliverables: "1x Feed Post + Link", deadline: "Sep 20, 2026"),
        BrandDealItem(id: "bd-3", brandName: "CRED Garage", campaignTitle: "Smart Vehicle Insurance & FASTag", payoutAmount: "₹1,20,000", deliverables: "1x Co-Authored Reel", deadline: "Oct 01, 2026")
    ]

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        Text("Brand Sponsorship Deals & Inquiries")
                            .font(.system(size: 13))
                            .foregroundColor(UsColors.textMuted)

                        LazyVStack(spacing: 12) {
                            ForEach(deals) { deal in
                                brandDealCard(deal)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Brand Collabs")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    @ViewBuilder
    private func brandDealCard(_ deal: BrandDealItem) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(deal.brandName)
                        .font(.system(size: 12, weight: .bold))
                        .foregroundColor(UsColors.postbookPrimary)

                    Text(deal.campaignTitle)
                        .font(.system(size: 15, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)
                }

                Spacer()

                Text(deal.payoutAmount)
                    .font(.system(size: 16, weight: .black, design: .rounded))
                    .foregroundColor(UsColors.onlineGreen)
            }

            Text("Deliverables: \(deal.deliverables) • Due \(deal.deadline)")
                .font(.system(size: 11))
                .foregroundColor(UsColors.textMuted)

            Divider().background(UsColors.borderSubtle)

            HStack {
                Button(action: {
                    ToastManager.shared.show("Brief downloaded", style: .info)
                }) {
                    Text("View Brief")
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                }

                Spacer()

                Button(action: {
                    HapticManager.shared.trigger(.success)
                    ToastManager.shared.show("Accepted deal with \(deal.brandName)! Escrow funded.", style: .success)
                }) {
                    Text("Accept Campaign")
                        .font(.system(size: 12, weight: .bold))
                        .foregroundColor(.black)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 6)
                        .background(Color.white)
                        .clipShape(Capsule())
                }
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
    }
}
