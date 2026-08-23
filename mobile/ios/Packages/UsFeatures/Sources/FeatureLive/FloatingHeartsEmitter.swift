import SwiftUI

public struct HeartParticle: Identifiable {
    public let id = UUID()
    public var x: CGFloat
    public var y: CGFloat
    public var scale: CGFloat
    public var opacity: Double
    public var color: Color
    public var sway: CGFloat
}

public struct FloatingHeartsEmitter: View {
    @Binding public var trigger: Int

    @State private var particles: [HeartParticle] = []
    private let colors: [Color] = [
        Color(red: 1.0, green: 0x33/255.0, blue: 0x66/255.0),
        Color(red: 1.0, green: 0x6B/255.0, blue: 0x35/255.0),
        Color(red: 0xC8/255.0, green: 0x50/255.0, blue: 0xC0/255.0),
        Color(red: 0x4E/255.0, green: 0xCD/255.0, blue: 0xC4/255.0)
    ]

    public init(trigger: Binding<Int>) {
        _trigger = trigger
    }

    public var body: some View {
        Canvas { context, size in
            for particle in particles {
                let rect = CGRect(
                    x: particle.x + sin(particle.sway) * 20,
                    y: particle.y,
                    width: 24 * particle.scale,
                    height: 24 * particle.scale
                )
                context.opacity = particle.opacity
                context.fill(
                    Path { path in
                        path.addEllipse(in: rect)
                    },
                    with: .color(particle.color)
                )
            }
        }
        .allowsHitTesting(false)
        .onChange(of: trigger) { _, _ in
            emitParticle()
        }
    }

    private func emitParticle() {
        let newParticle = HeartParticle(
            x: CGFloat.random(in: 260...320),
            y: 500,
            scale: CGFloat.random(in: 0.8...1.4),
            opacity: 1.0,
            color: colors.randomElement() ?? .pink,
            sway: CGFloat.random(in: 0...6.28)
        )
        particles.append(newParticle)

        withAnimation(.easeOut(duration: 2.5)) {
            if let idx = particles.firstIndex(where: { $0.id == newParticle.id }) {
                particles[idx].y -= 400
                particles[idx].opacity = 0.0
                particles[idx].sway += 4.0
            }
        }

        DispatchQueue.main.asyncAfter(deadline: .now() + 2.6) {
            particles.removeAll { $0.id == newParticle.id }
        }
    }
}
