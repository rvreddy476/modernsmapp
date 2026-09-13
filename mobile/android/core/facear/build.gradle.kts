// Virtual try-on (Face AR) on the Banuba SDK.
//
// READ FIRST: docs/COMMERCE-FACE-AR.md. It carries what the device taught us
// and what no vendor documentation says — the real effect-manifest format, why
// depending on Banuba's shipped prefabs does NOT work from an effect root, the
// black-screen trap an effect with no scene content causes, and how to
// diagnose any of it from logcat.
//
// The doc lives under docs/ rather than beside this file because the repo
// gitignores *.md everywhere except docs/, so a README here cannot be tracked.

plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
}

android {
    namespace = "com.us.android.core.facear"

    // The ONLY reason this module generates a BuildConfig: the try-on
    // diagnostics panel. It prints the licence state, the vendor's verbatim
    // error text and the effect's internals, and none of that may ever reach a
    // shipped build — so the panel is guarded on `BuildConfig.DEBUG`, which
    // means this module needs one of its own rather than a flag threaded down
    // from :app through DI and a composable parameter that a caller could get
    // wrong. The library's DEBUG follows the app's build type.
    buildFeatures { buildConfig = true }
}

// :core:facear owns ONE capability — putting a licensed Face AR effect on a
// live camera preview — and nothing about any product that uses it.
//
// It knows what a try-on IS (a kind, an effect slug, a variant with a colour)
// because that vocabulary has to be shared by the module that parses the wire
// and the module that draws the screen, and it knows how to turn a chosen
// variant into the effect's JS call. It does NOT know what a product is, does
// not fetch anything, and holds no repository.
//
// WHY THE VENDOR DEPENDENCIES ARE `implementation`
//
// Everything public here — FaceArState, the try-on domain, the JS-call
// builder, the composables — is expressible without a single Banuba type. So
// the SDK stays off every consumer's COMPILE classpath: :core:commerce can
// declare the descriptor on a Product without gaining the ability to call
// BanubaSdkManager, which is the same line :core:ui draws around the photo
// editor port.
dependencies {
    implementation(projects.core.common)
    // Theme tokens, UsIcons and the button/scaffold primitives the try-on
    // surface is built from. api, because the surface IS a composable that a
    // feature hosts inside its own UsTheme.
    api(projects.core.designsystem)
    // The three shared data states, so "no effect bundle" reads the way every
    // other empty state in the app reads.
    implementation(projects.core.ui)

    // Banuba Face AR SDK 1.17.6 — BanubaSdkManager, Effect, EffectManager,
    // LicenseManager. A DIFFERENT product line from the Video Editor SDK that
    // :feature:post declares (1.54.1), on the same licence token.
    //
    // THE AGGREGATOR IS TAKEN NON-TRANSITIVELY, AND THE PACKS ARE PINNED.
    //
    // `banuba_sdk` is the artifact to name — it is what the SDK calls itself
    // and its AAR carries nothing but a BuildConfig — but its runtime graph
    // asks for four packs Banuba's repository has never served into this
    // checkout: hands, body, acne_eyebags_removal, face_attributes. Taking it
    // transitively makes `--offline` resolution fail outright, and a networked
    // build would silently depend on artifacts nobody has seen. Those four are
    // for hand, body and skin-retouch effects; a FACE try-on needs none of
    // them.
    //
    // So the graph is stated instead of inherited: the two API artifacts, and
    // the native packs a face try-on genuinely needs. Every one of these is
    // already in the local Gradle cache, which is what keeps this module's
    // unit tests runnable offline.
    implementation(libs.banuba.face.ar.sdk) { isTransitive = false }
    implementation(libs.banuba.face.ar.api)
    implementation(libs.banuba.face.ar.core)
    runtimeOnly(libs.banuba.face.ar.effect.player)
    runtimeOnly(libs.banuba.face.ar.face.tracker)
    runtimeOnly(libs.banuba.face.ar.makeup)
    runtimeOnly(libs.banuba.face.ar.lips)
    runtimeOnly(libs.banuba.face.ar.eyes)
    runtimeOnly(libs.banuba.face.ar.hair)
    runtimeOnly(libs.banuba.face.ar.skin)
    runtimeOnly(libs.banuba.face.ar.background)
    runtimeOnly(libs.banuba.face.ar.light)
    // callJsMethod and evalJs are this pack. Without it a try-on loads an
    // effect and no shade ever changes.
    runtimeOnly(libs.banuba.face.ar.scripting)
    // The vendor's own effect unpacker (ArEffectsResourceManager), for the AR
    // Cloud effect source. Only the resource manager is used, and it is a
    // plain two-argument constructor — the cloud REPOSITORY needs the
    // vendor's Koin graph, which lives in :feature:post and must not be
    // started from here. See ArCloudTryOnEffects.
    //
    // NON-TRANSITIVE, and not as a shortcut. `ar-cloud` is a whole networked
    // effect-catalogue library: its POM asks for Retrofit 2.12.0, Room 2.7.2,
    // OkHttp 5.1.0, Moshi, ConstraintLayout and Material. This app already
    // uses HIGHER versions of all of them (Retrofit 3.0.0, Room 2.8.4, OkHttp
    // 5.4.0), and inside :feature:post — which has the rest of the app's
    // network stack on its classpath — conflict resolution picks those. Here
    // there is nothing to pick against, so Gradle would resolve ar-cloud's own
    // older requests and pull a second Retrofit and a Room compiler into a
    // module that does no networking and owns no database.
    //
    // What is needed from this artifact is one class with an
    // (AssetManager, File) constructor, which references none of that. The
    // runtime side is unchanged: :feature:post declares ar-cloud normally, so
    // the app still ships it with the app's own higher versions.
    implementation(libs.banuba.ar.cloud) { isTransitive = false }

    // rememberLauncherForActivityResult, for the CAMERA runtime permission.
    implementation(libs.androidx.activity.compose)
    implementation(libs.kotlinx.coroutines.core)

    testImplementation(libs.junit)
    testImplementation(libs.truth)
    testImplementation(libs.kotlinx.coroutines.test)
}
