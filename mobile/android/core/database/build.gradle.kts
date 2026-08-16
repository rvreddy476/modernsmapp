plugins {
    id("us.android.library")
    id("us.android.hilt")
    alias(libs.plugins.ksp)
}

android {
    namespace = "com.us.android.core.database"
}

// Room's generated schema JSON is checked in under core/database/schemas.
// It is what makes migration tests possible at all — without it there is
// nothing to migrate *from*. Phase 3 onwards will thank us.
//
// Note: the schema dir is NOT wired into androidTest assets yet. That hookup
// belongs with the first real migration test in Phase 2/3, and AGP 9's
// source-set DSL needs the new API for it.
ksp {
    arg("room.schemaLocation", "$projectDir/schemas")
    arg("room.generateKotlin", "true")
}

dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)

    api(libs.room.runtime)
    api(libs.room.ktx)
    ksp(libs.room.compiler)

    testImplementation(projects.core.testing)
    testImplementation(libs.room.testing)
}
