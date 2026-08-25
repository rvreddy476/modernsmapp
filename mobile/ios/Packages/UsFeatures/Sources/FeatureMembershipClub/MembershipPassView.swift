import SwiftUI
import UsModel
import UsDesignSystem

public struct MembershipPassView: View {
    public let creatorName: String
    public let memberTier: String
    public let passNumber: String
    public let onDismiss: () -> Void

    @State private var dragOffset: CGSize = .zero

    public init(
        creatorName: String = "Sarah Chen",
        memberTier: String = "VIP Founder Pass",
        passNumber: String = "US-MEM-0042-VIP",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.creatorName = creatorName
        self.memberTier = memberTier
        self.passNumber = passNumber
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 28) {
                    Text("Drag pass to tilt holographic badge")
                        .font(.system(size: 12))
                        .foregroundColor(UsColors.textMuted)

                    // 3D Holographic Membership Card
                    ZStack(alignment: .bottomLeading) {
                        // Background metallic foil gradient
                        LinearGradient(
                            colors: [
                                Color(red: 0xF2/255.0, green: 0xC9/255.0, blue: 0x4C/255.0),
                                Color(red: 0xF2/255.0, green: 0x99/255.0, blue: 0x4A/255.0),
                                Color(red: 0xEB/255.0, green: 0x57/255.0, blue: 0x57/255.0),
                                Color(red: 0xBB/255.0, green: 0x6B/255.0, blue: 0xD9/255.0)
                            ],
                            startPoint: UnitPoint(x: 0.0 + Double(dragOffset.width / 300), y: 0.0 + Double(dragOffset.height / 300)),
                            endPoint: UnitPoint(x: 1.0 + Double(dragOffset.width / 300), y: 1.0 + Double(dragOffset.height / 300))
                        )

                        // Card Content Overlays
                        VStack(alignment: .leading) {
                            HStack {
                                Text("US CLUB VIP")
                                    .font(.system(size: 16, weight: .black))
                                    .foregroundColor(.white)

                                Spacer()

                                Image(systemName: "sparkles")
                                    .font(.system(size: 20))
                                    .foregroundColor(.white)
                            }

                            Spacer()

                            VStack(alignment: .leading, spacing: 4) {
                                Text(memberTier.uppercased())
                                    .font(.system(size: 11, weight: .black))
                                    .foregroundColor(Color.black.opacity(0.8))

                                Text(creatorName)
                                    .font(.system(size: 22, weight: .bold))
                                    .foregroundColor(.white)

                                Text(passNumber)
                                    .font(.system(size: 12, weight: .bold, design: .monospaced))
                                    .foregroundColor(.white.opacity(0.9))
                            }
                        }
                        .padding(24)
                    }
                    .frame(width: 320, height: 200)
                    .clipShape(RoundedRectangle(cornerRadius: 24))
                    .overlay(RoundedRectangle(cornerRadius: 24).stroke(Color.white.opacity(0.4), lineWidth: 1.5))
                    .shadow(color: Color.orange.opacity(0.3), radius: 20, x: 0, y: 10)
                    .rotation3DEffect(
                        .degrees(Double(dragOffset.width / 10)),
                        axis: (x: 0, y: 1, z: 0)
                    )
                    .rotation3DEffect(
                        .degrees(Double(-dragOffset.height / 10)),
                        axis: (x: 1, y: 0, z: 0)
                    )
                    .gesture(
                        DragGesture()
                            .onChanged { val in
                                dragOffset = val.translation
                            }
                            .onEnded { _ in
                                withAnimation(.spring(response: 0.5, dampingFraction: 0.6)) {
                                    dragOffset = .zero
                                }
                            }
                    )

                    // Perks List
                    VStack(alignment: .leading, spacing: 10) {
                        Text("Club Perks Included")
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        perkRow("Unlimited backstage live audio room passes 🎙️")
                        perkRow("20% off all creator merchandise and event tickets 🎟️")
                        perkRow("Direct priority inbox DM replies 💬")
                    }
                    .padding(16)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(UsColors.bgSecondary)
                    .clipShape(RoundedRectangle(cornerRadius: 16))
                    .padding(.horizontal, 16)

                    Spacer()
                }
                .padding(.top, 16)
            }
            .navigationTitle("Membership Pass")
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
    private func perkRow(_ text: String) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "checkmark.seal.fill")
                .font(.system(size: 13))
                .foregroundColor(UsColors.onlineGreen)
            Text(text)
                .font(.system(size: 12))
                .foregroundColor(UsColors.textSecondary)
        }
    }
}
