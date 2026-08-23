import SwiftUI
import UsModel
import UsDesignSystem

public struct CountdownStickerView: View {
    public let eventTitle: String
    public let targetDate: Date
    public let onRemindMe: () -> Void

    @State private var remainingTime: (days: Int, hours: Int, mins: Int, secs: Int) = (3, 14, 28, 45)
    @State private var hasSubscribedReminder: Bool = false

    public init(
        eventTitle: String = "🚀 Super-App 2.0 Launch",
        targetDate: Date = Date().addingTimeInterval(3 * 86400 + 14 * 3600),
        onRemindMe: @escaping () -> Void = {}
    ) {
        self.eventTitle = eventTitle
        self.targetDate = targetDate
        self.onRemindMe = onRemindMe
    }

    public var body: some View {
        VStack(spacing: 12) {
            Text(eventTitle)
                .font(.system(size: 15, weight: .bold))
                .foregroundColor(.white)
                .multilineTextAlignment(.center)

            // Countdown digits grid
            HStack(spacing: 8) {
                timeBox(value: remainingTime.days, label: "DAYS")
                Text(":").font(.system(size: 16, weight: .bold)).foregroundColor(.white)
                timeBox(value: remainingTime.hours, label: "HOURS")
                Text(":").font(.system(size: 16, weight: .bold)).foregroundColor(.white)
                timeBox(value: remainingTime.mins, label: "MINS")
                Text(":").font(.system(size: 16, weight: .bold)).foregroundColor(.white)
                timeBox(value: remainingTime.secs, label: "SECS")
            }

            // Remind Me button
            Button(action: {
                hasSubscribedReminder.toggle()
                HapticManager.shared.trigger(.success)
                if hasSubscribedReminder {
                    ToastManager.shared.show("🔔 Reminder set for \(eventTitle)!", style: .success)
                    onRemindMe()
                }
            }) {
                HStack(spacing: 6) {
                    Image(systemName: hasSubscribedReminder ? "bell.fill" : "bell")
                    Text(hasSubscribedReminder ? "Reminder Set" : "Remind Me")
                }
                .font(.system(size: 12, weight: .bold))
                .foregroundColor(hasSubscribedReminder ? .black : .white)
                .padding(.horizontal, 14)
                .padding(.vertical, 6)
                .background(hasSubscribedReminder ? Color.white : Color.white.opacity(0.2))
                .clipShape(Capsule())
            }
            .buttonStyle(.plain)
        }
        .padding(16)
        .background(
            LinearGradient(
                colors: [Color(red: 0x6A/255.0, green: 0x11/255.0, blue: 0xCB/255.0), Color(red: 0x25/255.0, green: 0x75/255.0, blue: 0xFC/255.0)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 20))
        .shadow(radius: 8)
        .frame(width: 280)
        .onAppear {
            Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { _ in
                if remainingTime.secs > 0 {
                    remainingTime.secs -= 1
                } else {
                    remainingTime.secs = 59
                    if remainingTime.mins > 0 { remainingTime.mins -= 1 }
                }
            }
        }
    }

    @ViewBuilder
    private func timeBox(value: Int, label: String) -> some View {
        VStack(spacing: 2) {
            Text(String(format: "%02d", value))
                .font(.system(size: 16, weight: .black, design: .monospaced))
                .foregroundColor(.white)
                .padding(.horizontal, 6)
                .padding(.vertical, 4)
                .background(Color.black.opacity(0.3))
                .clipShape(RoundedRectangle(cornerRadius: 6))

            Text(label)
                .font(.system(size: 8, weight: .bold))
                .foregroundColor(.white.opacity(0.8))
        }
    }
}
