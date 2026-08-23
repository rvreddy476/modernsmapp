import SwiftUI
import UsModel
import UsDesignSystem

public struct WeatherMoodStickerView: View {
    public let city: String
    public let tempCelsius: Int
    public let condition: String

    @State private var moodScale: CGFloat = 1.0

    public init(
        city: String = "Bengaluru",
        tempCelsius: Int = 24,
        condition: String = "Partly Cloudy ⛅️"
    ) {
        self.city = city
        self.tempCelsius = tempCelsius
        self.condition = condition
    }

    public var body: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(city.uppercased())
                    .font(.system(size: 10, weight: .black))
                    .foregroundColor(Color.white.opacity(0.8))

                Text("\(tempCelsius)°C")
                    .font(.system(size: 24, weight: .black, design: .rounded))
                    .foregroundColor(.white)

                Text(condition)
                    .font(.system(size: 11, weight: .medium))
                    .foregroundColor(.white.opacity(0.9))
            }

            Spacer()

            Button(action: {
                withAnimation(.spring(response: 0.3, dampingFraction: 0.6)) {
                    moodScale = 1.3
                }
                HapticManager.shared.trigger(.selection)
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
                    withAnimation(.spring()) {
                        moodScale = 1.0
                    }
                }
            }) {
                Text("😎")
                    .font(.system(size: 32))
                    .scaleEffect(moodScale)
            }
            .buttonStyle(.plain)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .background(
            LinearGradient(
                colors: [Color.blue.opacity(0.85), Color.purple.opacity(0.85)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 18))
        .overlay(RoundedRectangle(cornerRadius: 18).stroke(Color.white.opacity(0.3), lineWidth: 1))
        .shadow(color: Color.black.opacity(0.3), radius: 8, x: 0, y: 4)
        .frame(width: 260)
    }
}
