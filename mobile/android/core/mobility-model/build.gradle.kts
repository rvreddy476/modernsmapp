plugins {
    id("us.jvm.library")
}

// Deliberately empty. :core:mobility-model is pure Kotlin/JVM and must stay
// that way (Mopedu, 2026-09-18, ported from the Gemini branch).
//
// It carries no Android dependency, no serialization annotations and no
// third-party types: the ride, quote, payment-status and captain domain, all
// money in integer paise, plus the pure rules both features share — surge
// display, the cancellation-fee window, the receipt lines. DTOs live in each
// feature's data layer. Enforced by the moduleGraphCheck task.
//
// :core:testing is an Android library, so the JVM test dependencies are named
// here exactly as :core:model names them.
dependencies {
    testImplementation(libs.junit)
    testImplementation(libs.truth)
}
