import SwiftUI
import UsModel
import UsDesignSystem

public struct HuddleParticipant: Identifiable {
    public let id: String
    public let name: String
    public let isSpeaking: Bool

    public init(id: String, name: String, isSpeaking: Bool = false) {
        self.id = id
        self.name = name
        self.isSpeaking = isSpeaking
    }
}

public struct ChatHuddleView: View {
    @State private var isJoined: Bool = false
    @State private var participants: [HuddleParticipant] = [
        HuddleParticipant(id: "hp-1", name: "Alex Rivera", isSpeaking: true),
        HuddleParticipant(id: "hp-2", name: "Sarah Chen", isSpeaking: false),
        HuddleParticipant(id: "hp-3", name: "Marcus Vance", isSpeaking: true)
    ]

    public init() {}

    public var body: some View {
        HStack(spacing: 12) {
            HStack(spacing: 6) {
                Circle()
                    .fill(UsColors.onlineGreen)
                    .frame(width: 10, height: 10)

                Image(systemName: "headphones")
                    .foregroundColor(.white)
                    .font(.system(size: 13, weight: .bold))

                Text("Huddle Active")
                    .font(.system(size: 12, weight: .bold))
                    .foregroundColor(.white)
            }

            // Participant avatars
            HStack(spacing: -8) {
                ForEach(participants) { participant in
                    UsAvatar(name: participant.name, size: .small)
                        .overlay(
                            participant.isSpeaking ?
                                Circle().stroke(UsColors.onlineGreen, lineWidth: 2) :
                                Circle().stroke(Color.black, lineWidth: 1.5)
                        )
                }
            }

            Spacer()

            // Join / Leave Button
            Button(action: {
                isJoined.toggle()
                HapticManager.shared.trigger(.success)
                ToastManager.shared.show(isJoined ? "Joined Voice Huddle 🎧" : "Left Voice Huddle", style: .info)
            }) {
                Text(isJoined ? "Leave" : "Join Huddle")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundColor(isJoined ? UsColors.liveRed : .black)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 6)
                    .background(isJoined ? UsColors.liveRed.opacity(0.15) : Color.white)
                    .clipShape(Capsule())
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .background(Color(red: 0x1A/255.0, green: 0x1E/255.0, blue: 0x2A/255.0))
        .clipShape(RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(UsColors.onlineGreen.opacity(0.4), lineWidth: 1))
        .padding(.horizontal, 16)
    }
}
