plugins {
    id("us.jvm.library")
}

// Deliberately empty. :core:mobility-model is pure Kotlin/JVM.
//
// It carries no Android dependency, no serialization annotations, and no
// third-party types. DTOs live in network layer; domain models live here.
// Enforced by the moduleGraphCheck task.
dependencies {
    testImplementation(libs.junit)
    testImplementation(libs.truth)
}
