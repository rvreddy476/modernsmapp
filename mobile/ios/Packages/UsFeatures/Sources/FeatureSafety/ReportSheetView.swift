import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public enum ReportReason: String, CaseIterable, Identifiable {
    case spam = "Spam or Scams"
    case harassment = "Harassment or Bullying"
    case hateSpeech = "Hate Speech or Violence"
    case nudity = "Nudity or Sexual Content"
    case copyright = "Intellectual Property Violation"
    case misinformation = "False Information"
    case other = "Something else"

    public var id: String { rawValue }
}

public struct ReportSheetView: View {
    public let targetId: String
    public let targetType: String // "post", "user", "comment", "reel"
    public let onDismiss: () -> Void

    @State private var selectedReason: ReportReason? = nil
    @State private var additionalDetails: String = ""
    @State private var isSubmitting: Bool = false

    public init(
        targetId: String,
        targetType: String = "post",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.targetId = targetId
        self.targetType = targetType
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 20) {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Why are you reporting this \(targetType)?")
                            .font(.system(size: 17, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)
                        Text("Your report is confidential and helps keep US safe for everyone.")
                            .font(.system(size: 13))
                            .foregroundColor(UsColors.textMuted)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)

                    List(ReportReason.allCases) { reason in
                        Button(action: { selectedReason = reason }) {
                            HStack {
                                Text(reason.rawValue)
                                    .font(.system(size: 15))
                                    .foregroundColor(UsColors.textPrimary)
                                Spacer()
                                if selectedReason == reason {
                                    Image(systemName: "checkmark")
                                        .foregroundColor(UsColors.postbookPrimary)
                                }
                            }
                        }
                        .listRowBackground(UsColors.bgSecondary)
                    }
                    .listStyle(.plain)
                    .scrollContentBackground(.hidden)

                    Button(action: submitReport) {
                        HStack {
                            Spacer()
                            if isSubmitting {
                                ProgressView().tint(.black)
                            } else {
                                Text("Submit Report")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(.black)
                            }
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(selectedReason != nil ? Color.white : Color.white.opacity(0.3))
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .disabled(selectedReason == nil || isSubmitting)
                }
                .padding(16)
            }
            .navigationTitle("Report")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private func submitReport() {
        isSubmitting = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
            isSubmitting = false
            ToastManager.shared.show("Thank you for your report. Our Trust & Safety team will review it.", style: .info)
            onDismiss()
        }
    }
}
