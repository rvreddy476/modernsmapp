plugins {
    id("us.jvm.library")
    alias(libs.plugins.kotlin.serialization)
}

// :core:creator-model is PURE Kotlin/JVM and must stay that way.
//
// It owns the frozen AndroidCreatorProject v1 contract: the document model,
// canonicalization, validators, and the RenderExporter port. Nothing here may
// import android.* or androidx.* — that is what keeps the canonical bytes
// unit-testable on the JVM, and what lets :core:media implement the render port
// without :core:creator-engine ever depending on :core:media. Enforced by
// moduleGraphCheck guards G-2, G-4 and G-5.
dependencies {
    api(libs.kotlinx.serialization.json)

    testImplementation(libs.junit)
    testImplementation(libs.truth)
}
