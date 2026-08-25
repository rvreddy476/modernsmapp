plugins {
    id("us.android.library")
    id("us.android.hilt")
}

android {
    namespace = "com.us.android.core.creator.engine"
}

// Orchestration only: autosave, the source vault, legacy adoption and the one
// publisher. It calls the RenderExporter PORT declared in :core:creator-model
// and must never depend on :core:media, which implements that port — app DI
// binds the two. Guards G-4/G-5 in the root build assert both directions.
dependencies {
    api(projects.core.creatorModel)
    implementation(projects.core.database)
    implementation(projects.core.common)
    implementation(projects.core.model)

    testImplementation(projects.core.testing)
}
