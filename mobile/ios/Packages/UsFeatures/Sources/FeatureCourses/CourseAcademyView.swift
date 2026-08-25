import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct CourseLessonItem: Identifiable {
    public let id: String
    public let title: String
    public let duration: String
    public var isCompleted: Bool

    public init(id: String, title: String, duration: String, isCompleted: Bool = false) {
        self.id = id
        self.title = title
        self.duration = duration
        self.isCompleted = isCompleted
    }
}

public struct CourseAcademyView: View {
    public let onDismiss: () -> Void

    @State private var courseTitle: String = "Full-Stack iOS SwiftUI & Go Microservices Architecture"
    @State private var instructorName: String = "Alex Rivera & Sarah Chen"
    @State private var lessons: [CourseLessonItem] = [
        CourseLessonItem(id: "les-1", title: "1. Multi-Package SPM & Domain Architecture", duration: "18:24", isCompleted: true),
        CourseLessonItem(id: "les-2", title: "2. Ultra Low-Latency WebSockets & URLSession", duration: "24:10", isCompleted: true),
        CourseLessonItem(id: "les-3", title: "3. Go Microservices Mesh & Redis Idempotency", duration: "32:45", isCompleted: false),
        CourseLessonItem(id: "les-4", title: "4. AVPlayer Media Preloading Pipeline & Scrubbing", duration: "21:15", isCompleted: false)
    ]

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    private var completedCount: Int {
        lessons.filter { $0.isCompleted }.count
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        // Video Player Preview
                        ZStack(alignment: .bottomLeading) {
                            Rectangle()
                                .fill(Color(red: 0x14/255.0, green: 0x14/255.0, blue: 0x22/255.0))

                            VStack(alignment: .leading, spacing: 4) {
                                Text("Now Playing: Lesson 3")
                                    .font(.system(size: 11, weight: .bold))
                                    .foregroundColor(UsColors.postbookPrimary)
                                Text("Go Microservices Mesh & Redis Idempotency")
                                    .font(.system(size: 14, weight: .bold))
                                    .foregroundColor(.white)
                            }
                            .padding(14)
                        }
                        .frame(height: 200)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        // Progress summary
                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Text("Course Progress")
                                    .font(.system(size: 13, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)

                                Spacer()

                                Text("\(completedCount) / \(lessons.count) Completed")
                                    .font(.system(size: 12, weight: .semibold))
                                    .foregroundColor(UsColors.onlineGreen)
                            }

                            Capsule()
                                .fill(UsColors.bgTertiary)
                                .frame(height: 6)
                                .overlay(
                                    GeometryReader { geo in
                                        Capsule()
                                            .fill(UsColors.onlineGreen)
                                            .frame(width: geo.size.width * CGFloat(completedCount) / CGFloat(lessons.count), height: 6)
                                    },
                                    alignment: .leading
                                )
                        }
                        .padding(14)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 14))

                        // Curriculum
                        Text("Course Curriculum")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 10) {
                            ForEach($lessons) { $lesson in
                                lessonRow(lesson: $lesson)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Academy Masterclass")
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
    private func lessonRow(lesson: Binding<CourseLessonItem>) -> some View {
        Button(action: {
            lesson.wrappedValue.isCompleted.toggle()
            HapticManager.shared.trigger(.selection)
            ToastManager.shared.show(lesson.wrappedValue.isCompleted ? "Marked as completed!" : "Lesson active", style: .info)
        }) {
            HStack(spacing: 12) {
                Image(systemName: lesson.wrappedValue.isCompleted ? "checkmark.circle.fill" : "play.circle.fill")
                    .font(.system(size: 24))
                    .foregroundColor(lesson.wrappedValue.isCompleted ? UsColors.onlineGreen : UsColors.postbookPrimary)

                VStack(alignment: .leading, spacing: 2) {
                    Text(lesson.wrappedValue.title)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                        .lineLimit(1)

                    Text(lesson.wrappedValue.duration)
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()
            }
            .padding(12)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 12))
        }
        .buttonStyle(.plain)
    }
}
