import SwiftUI
import UsModel
import UsDesignSystem

public struct AddYoursStickerView: View {
    public let promptTitle: String
    public let participantCount: Int
    public let onAddYours: () -> Void

    public init(
        promptTitle: String = "Drop your golden hour sunset 🌅",
        participantCount: Int = 1420,
        onAddYours: @escaping () -> Void = {}
    ) {
        self.promptTitle = promptTitle
        self.participantCount = participantCount
        self.onAddYours = onAddYours
    }

    public var body: some View {
        VStack(spacing: 8) {
            HStack(spacing: 6) {
                Image(systemName: "camera.circle.fill")
                    .foregroundColor(.white)
                    .font(.system(size: 16))

                Text("Add Yours")
                    .font(.system(size: 12, weight: .black))
                    .foregroundColor(.white)
            }

            Text(promptTitle)
                .font(.system(size: 14, weight: .bold))
                .foregroundColor(.white)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 8)

            HStack(spacing: -6) {
                Circle().fill(Color.orange).frame(width: 22, height: 22)
                    .overlay(Text("🌅").font(.system(size: 10)))
                Circle().fill(Color.purple).frame(width: 22, height: 22)
                    .overlay(Text("🏖️").font(.system(size: 10)))
                Circle().fill(Color.blue).frame(width: 22, height: 22)
                    .overlay(Text("✈️").font(.system(size: 10)))

                Text("\(participantCount)+ added")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundColor(.white)
                    .padding(.leading, 12)
            }

            Button(action: {
                HapticManager.shared.trigger(.success)
                ToastManager.shared.show("Opening camera for '\(promptTitle)'", style: .info)
                onAddYours()
            }) {
                HStack(spacing: 4) {
                    Image(systemName: "plus")
                    Text("Add Yours")
                }
                .font(.system(size: 12, weight: .bold))
                .foregroundColor(.black)
                .padding(.horizontal, 16)
                .padding(.vertical, 8)
                .background(Color.white)
                .clipShape(Capsule())
            }
            .padding(.top, 4)
        }
        .padding(14)
        .background(Color.black.opacity(0.8))
        .clipShape(RoundedRectangle(cornerRadius: 18))
        .overlay(RoundedRectangle(cornerRadius: 18).stroke(Color.white.opacity(0.25), lineWidth: 1))
        .frame(width: 260)
    }
}
