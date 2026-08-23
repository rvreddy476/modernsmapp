import SwiftUI
import UsModel
import UsDesignSystem

public struct ScheduledDMItem: Identifiable {
    public let id: String
    public let recipientName: String
    public let messageText: String
    public let scheduledDate: String

    public init(id: String, recipientName: String, messageText: String, scheduledDate: String) {
        self.id = id
        self.recipientName = recipientName
        self.messageText = messageText
        self.scheduledDate = scheduledDate
    }
}

public struct ScheduledMessageView: View {
    public let onDismiss: () -> Void

    @State private var messageText: String = "Happy Birthday! 🎉 Hope you have an awesome year ahead!"
    @State private var selectedDate: Date = Date().addingTimeInterval(86400)
    @State private var scheduledItems: [ScheduledDMItem] = [
        ScheduledDMItem(id: "sdm-1", recipientName: "Sarah Chen", messageText: "Happy Birthday Sarah! 🎂 Wishing you the best day!", scheduledDate: "Tomorrow at 12:00 AM"),
        ScheduledDMItem(id: "sdm-2", recipientName: "Marcus Vance", messageText: "Reminder: Review the Super-App PR sprint at 10 AM", scheduledDate: "Monday at 09:30 AM")
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
                    VStack(alignment: .leading, spacing: 20) {
                        // Composer Card
                        VStack(alignment: .leading, spacing: 12) {
                            Text("Schedule New Message")
                                .font(.system(size: 14, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            TextField("Write message...", text: $messageText)
                                .textFieldStyle(.plain)
                                .padding(14)
                                .background(UsColors.bgTertiary)
                                .clipShape(RoundedRectangle(cornerRadius: 12))
                                .foregroundColor(UsColors.textPrimary)

                            DatePicker("Send At", selection: $selectedDate, in: Date()...)
                                .datePickerStyle(.compact)
                                .foregroundColor(UsColors.textPrimary)
                                .tint(UsColors.postbookPrimary)

                            Button(action: scheduleMessage) {
                                HStack {
                                    Spacer()
                                    Text("Schedule Message")
                                        .font(.system(size: 14, weight: .bold))
                                        .foregroundColor(.black)
                                    Spacer()
                                }
                                .padding(.vertical, 12)
                                .background(Color.white)
                                .clipShape(RoundedRectangle(cornerRadius: 12))
                            }
                            .disabled(messageText.isEmpty)
                        }
                        .padding(16)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        // Scheduled Queue
                        Text("Queued Messages (\(scheduledItems.count))")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 10) {
                            ForEach(scheduledItems) { item in
                                scheduledRow(item)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Scheduled DMs")
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
    private func scheduledRow(_ item: ScheduledDMItem) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text("To: \(item.recipientName)")
                    .font(.system(size: 13, weight: .bold))
                    .foregroundColor(UsColors.postbookPrimary)

                Spacer()

                Text(item.scheduledDate)
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)
            }

            Text(item.messageText)
                .font(.system(size: 13))
                .foregroundColor(UsColors.textPrimary)
                .lineLimit(2)
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }

    private func scheduleMessage() {
        HapticManager.shared.trigger(.success)
        let newItem = ScheduledDMItem(
            id: UUID().uuidString,
            recipientName: "Selected Contact",
            messageText: messageText,
            scheduledDate: "Scheduled for Send"
        )
        scheduledItems.append(newItem)
        messageText = ""
        ToastManager.shared.show("Message scheduled successfully!", style: .success)
    }
}
