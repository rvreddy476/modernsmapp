plugins {
    id("us.jvm.library")
}

// Deliberately empty. :core:model is pure Kotlin/JVM and must stay that way.
//
// It carries no Android dependency, no serialization annotations, and no
// third-party types. DTOs live in :core:network; domain models live here.
// If something here needs an Android import, the design is wrong — fix the
// design, not this file. Enforced by the moduleGraphCheck task in the root
// build and by CI job 6.
dependencies {
    testImplementation(libs.junit)
    testImplementation(libs.truth)
}
