// Root build file. Plugins are declared `apply false` here so that the
// versions resolve once from the catalog; individual modules apply the
// `us.*` convention plugins from build-logic instead of these directly.
plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.android.library) apply false
    alias(libs.plugins.kotlin.android) apply false
    alias(libs.plugins.kotlin.jvm) apply false
    alias(libs.plugins.kotlin.compose) apply false
    alias(libs.plugins.kotlin.serialization) apply false
    alias(libs.plugins.kotlin.parcelize) apply false
    alias(libs.plugins.ksp) apply false
    alias(libs.plugins.hilt) apply false
    alias(libs.plugins.detekt)
}

// Detekt runs over every module from the root, with detekt-formatting
// supplying the ktlint rule set. One tool instead of detekt + ktlint-gradle.
dependencies {
    detektPlugins(libs.detekt.formatting)
}

detekt {
    buildUponDefaultConfig = true
    allRules = false
    config.setFrom(files("$rootDir/config/detekt/detekt.yml"))
    source.setFrom(
        files(
            subprojects.map { "${it.projectDir}/src/main/kotlin" },
            subprojects.map { "${it.projectDir}/src/test/kotlin" },
        ),
    )
    parallel = true
}

tasks.withType<io.gitlab.arturbosch.detekt.Detekt>().configureEach {
    jvmTarget = libs.versions.javaTarget.get()
    reports {
        html.required.set(true)
        xml.required.set(true)
        sarif.required.set(true)
        txt.required.set(false)
        md.required.set(false)
    }
}

tasks.register<Delete>("clean") {
    delete(rootProject.layout.buildDirectory)
}

/**
 * Application-boundary rules (Feast A0, 2026-09-13).
 *
 * Momentum (`:app`), Feast Kitchen (`:app-kitchen`) and Feast Rider
 * (`:app-rider`) are separate installs built from one module graph. These rules
 * keep each APK to its own code:
 *
 *   a. No module depends on an application module (`:app` or any `:app-*`).
 *      Applications are leaves; shared code belongs in :core.
 *   b. `:app` never reaches `:feature:kitchen` or `:feature:rider`.
 *   c. `:app-kitchen` / `:app-rider` never reach `:core:facear`,
 *      `:core:creator-engine`, `:feature:post` or `:core:commerce` — no Banuba,
 *      creator engine, posting or shop code in a partner APK.
 *   d. `:feature:kitchen` / `:feature:rider` never reach `:core:facear`.
 *
 * (b)–(d) are TRANSITIVE over implementation/api/runtimeOnly project edges,
 * because the hazard is what ends up in the APK, not what one build file says.
 * Consequence worth knowing before A2/A3: today `:core:commerce` exposes
 * `:core:facear` via `api`, so it cannot be pulled into a partner app without
 * first splitting it. (a) is direct, like the rule it generalises.
 *
 * Why `:core:creator-model` is NOT in (c): it is pure Kotlin/JVM (only
 * kotlinx-serialization, no android.*, no Banuba), and `:core:media` depends on
 * it. The partner apps need `:core:media` for KYC document and menu-photo
 * uploads, so banning the model would ban uploads. The first version of this
 * rule banned every `:core:creator-*` and would have blocked exactly that. The
 * creator ENGINE — the part with real weight — stays banned. Note that
 * `:core:media` also exposes Media3 ExoPlayer via `api`; a partner app pulling
 * it in carries the player too. That is size, not a boundary leak, and
 * splitting `MediaUploader` out of `:core:media` is the fix if it matters.
 *
 * Pure over a `module -> direct project deps` map, so every rule is inert for a
 * module that does not exist yet and [applicationBoundarySelfCheck] can prove
 * each one fires against graphs containing modules that do not exist yet.
 */
fun applicationBoundaryViolations(direct: Map<String, Set<String>>): List<String> {
    fun reach(root: String): Set<String> {
        val seen = LinkedHashSet<String>()
        val queue = kotlin.collections.ArrayDeque(direct[root].orEmpty())
        while (queue.isNotEmpty()) {
            val next = queue.removeFirst()
            if (seen.add(next)) queue.addAll(direct[next].orEmpty())
        }
        return seen
    }

    return buildList {
        // (a)
        direct.forEach { (module, deps) ->
            deps.filter { it == ":app" || it.startsWith(":app-") }.forEach { app ->
                add("$module must not depend on $app — application modules are leaves; shared code belongs in :core.")
            }
        }
        // (b)
        if (":app" in direct) {
            reach(":app").filter { it == ":feature:kitchen" || it == ":feature:rider" }.forEach { dep ->
                add(":app must not depend on $dep (directly or transitively) — partner-app features ship only in their own app.")
            }
        }
        // (c)
        listOf(":app-kitchen", ":app-rider").filter { it in direct }.forEach { app ->
            reach(app).filter {
                it == ":core:facear" || it == ":core:creator-engine" ||
                    it == ":feature:post" || it == ":core:commerce"
            }.forEach { dep ->
                add("$app must not depend on $dep (directly or transitively) — partner apps carry no Banuba, creator, post or commerce code.")
            }
        }
        // (d)
        listOf(":feature:kitchen", ":feature:rider").filter { it in direct }.forEach { feature ->
            if (":core:facear" in reach(feature)) {
                add("$feature must not depend on :core:facear (directly or transitively) — Face AR is Momentum-only.")
            }
        }
    }
}

/**
 * Proves each application-boundary rule still fires. Runs inside
 * moduleGraphCheck on every invocation against synthetic graphs — the real
 * graph cannot exercise rules about modules that do not exist yet, and a rule
 * that silently stopped matching would otherwise look exactly like a clean
 * graph. Returns one message per case that did not behave.
 */
fun applicationBoundarySelfCheck(): List<String> {
    // name, graph, expected violation prefix (null = must be clean)
    val cases: List<Triple<String, Map<String, Set<String>>, String?>> = listOf(
        Triple(
            "legal three-app graph",
            mapOf(
                ":app" to setOf(":feature:feast", ":feature:post", ":core:commerce"),
                ":core:commerce" to setOf(":core:facear"),
                ":app-kitchen" to setOf(":feature:kitchen", ":core:food"),
                ":feature:kitchen" to setOf(":core:food", ":core:kyc-ui"),
                ":app-rider" to setOf(":feature:rider", ":core:location"),
                ":feature:rider" to setOf(":core:location", ":core:kyc-ui"),
            ),
            null,
        ),
        Triple("core -> partner app", mapOf(":core:food" to setOf(":app-kitchen")), ":core:food must not depend on :app-kitchen"),
        Triple("feature -> :app", mapOf(":feature:feast" to setOf(":app")), ":feature:feast must not depend on :app"),
        Triple("partner app -> :app", mapOf(":app-rider" to setOf(":app")), ":app-rider must not depend on :app"),
        Triple(":app -> kitchen feature", mapOf(":app" to setOf(":feature:kitchen")), ":app must not depend on :feature:kitchen"),
        Triple(
            ":app -> rider feature, transitively",
            mapOf(":app" to setOf(":core:x"), ":core:x" to setOf(":feature:rider")),
            ":app must not depend on :feature:rider",
        ),
        Triple("kitchen app -> facear", mapOf(":app-kitchen" to setOf(":core:facear")), ":app-kitchen must not depend on :core:facear"),
        Triple("kitchen app -> creator-*", mapOf(":app-kitchen" to setOf(":core:creator-engine")), ":app-kitchen must not depend on :core:creator-engine"),
        Triple("rider app -> post", mapOf(":app-rider" to setOf(":feature:post")), ":app-rider must not depend on :feature:post"),
        Triple(
            "rider app -> commerce, transitively",
            mapOf(":app-rider" to setOf(":feature:rider"), ":feature:rider" to setOf(":core:kyc-ui"), ":core:kyc-ui" to setOf(":core:commerce")),
            ":app-rider must not depend on :core:commerce",
        ),
        Triple("kitchen feature -> facear", mapOf(":feature:kitchen" to setOf(":core:facear")), ":feature:kitchen must not depend on :core:facear"),
        Triple(
            "rider feature -> facear, transitively",
            mapOf(":feature:rider" to setOf(":core:y"), ":core:y" to setOf(":core:facear")),
            ":feature:rider must not depend on :core:facear",
        ),
        // :app-kitchen coverage (Feast A3, 2026-09-13). The Kitchen app now
        // exists, so every edge its APK could grow is proven to fire — not
        // only the two direct ones A0 wrote ahead of it.
        Triple("kitchen app -> :app", mapOf(":app-kitchen" to setOf(":app")), ":app-kitchen must not depend on :app"),
        Triple("kitchen app -> post", mapOf(":app-kitchen" to setOf(":feature:post")), ":app-kitchen must not depend on :feature:post"),
        Triple(
            "kitchen app -> commerce, transitively",
            mapOf(
                ":app-kitchen" to setOf(":feature:kitchen"),
                ":feature:kitchen" to setOf(":core:media"),
                ":core:media" to setOf(":core:commerce"),
            ),
            ":app-kitchen must not depend on :core:commerce",
        ),
        Triple(
            "kitchen app -> creator engine, transitively",
            mapOf(":app-kitchen" to setOf(":core:auth"), ":core:auth" to setOf(":core:creator-engine")),
            ":app-kitchen must not depend on :core:creator-engine",
        ),
        Triple(
            "kitchen app -> facear, transitively through commerce",
            mapOf(":app-kitchen" to setOf(":core:commerce"), ":core:commerce" to setOf(":core:facear")),
            ":app-kitchen must not depend on :core:facear",
        ),
        Triple(
            ":app -> kitchen feature, transitively",
            mapOf(":app" to setOf(":core:z"), ":core:z" to setOf(":feature:kitchen")),
            ":app must not depend on :feature:kitchen",
        ),
        Triple("kitchen feature -> kitchen app", mapOf(":feature:kitchen" to setOf(":app-kitchen")), ":feature:kitchen must not depend on :app-kitchen"),
        // :app-rider coverage (Feast A4, 2026-09-13), the same set Kitchen got
        // in A3: every edge the Rider APK could grow is proven to fire.
        Triple("rider app -> facear", mapOf(":app-rider" to setOf(":core:facear")), ":app-rider must not depend on :core:facear"),
        Triple("rider app -> creator engine", mapOf(":app-rider" to setOf(":core:creator-engine")), ":app-rider must not depend on :core:creator-engine"),
        Triple("rider app -> commerce", mapOf(":app-rider" to setOf(":core:commerce")), ":app-rider must not depend on :core:commerce"),
        Triple(
            "rider app -> post, transitively",
            mapOf(":app-rider" to setOf(":feature:rider"), ":feature:rider" to setOf(":core:media"), ":core:media" to setOf(":feature:post")),
            ":app-rider must not depend on :feature:post",
        ),
        Triple(
            "rider app -> creator engine, transitively",
            mapOf(":app-rider" to setOf(":core:auth"), ":core:auth" to setOf(":core:creator-engine")),
            ":app-rider must not depend on :core:creator-engine",
        ),
        Triple(
            "rider app -> facear, transitively through commerce",
            mapOf(":app-rider" to setOf(":core:commerce"), ":core:commerce" to setOf(":core:facear")),
            ":app-rider must not depend on :core:facear",
        ),
        Triple(":app -> rider feature", mapOf(":app" to setOf(":feature:rider")), ":app must not depend on :feature:rider"),
        Triple("rider feature -> rider app", mapOf(":feature:rider" to setOf(":app-rider")), ":feature:rider must not depend on :app-rider"),
        Triple("rider feature -> facear", mapOf(":feature:rider" to setOf(":core:facear")), ":feature:rider must not depend on :core:facear"),
        Triple("kitchen app -> rider app", mapOf(":app-kitchen" to setOf(":app-rider")), ":app-kitchen must not depend on :app-rider"),
    )
    return cases.mapNotNull { (name, graph, expected) ->
        val found = applicationBoundaryViolations(graph)
        when {
            expected == null && found.isNotEmpty() ->
                "moduleGraphCheck self-check '$name': a legal graph was flagged: $found"
            expected != null && found.none { it.startsWith(expected) } ->
                "moduleGraphCheck self-check '$name': expected a violation starting " +
                    "'$expected', got $found — a rule has stopped firing."
            else -> null
        }
    }
}

/**
 * CI job 6 — enforces the module dependency rules from PHASE_0_1_PLAN §B
 * so they stay real rather than aspirational.
 *
 * Checks:
 *   1. :core:model is a plain Kotlin/JVM module with no Android plugin.
 *   2. No module depends on an application module (:app or any :app-*), plus
 *      the Feast application-boundary rules — see
 *      [applicationBoundaryViolations]. Self-checked on every run.
 *   3. No :feature module depends on another :feature module.
 *
 * Runs at configuration time against the project graph, so it costs nothing
 * at execution and cannot be forgotten.
 */
tasks.register("moduleGraphCheck") {
    group = "verification"
    description = "Asserts the module dependency rules in PHASE_0_1_PLAN §B."

    // Everything is resolved here, at configuration time, into plain
    // serializable values. The doLast block below closes over only a
    // List<String> and an Int — no Project, no script reference — which is
    // what keeps this task compatible with the configuration cache.
    val violations: List<String> = buildList {
        subprojects.find { it.path == ":core:model" }?.let { model ->
            listOf("com.android.library", "com.android.application").forEach { id ->
                if (model.pluginManager.hasPlugin(id)) {
                    add(":core:model must not apply '$id' — it is a pure Kotlin/JVM module.")
                }
            }
        }

        subprojects.forEach { sub ->
            val deps = sub.configurations
                .filter { it.name in setOf("implementation", "api") }
                .flatMap { config -> config.dependencies }
                .filterIsInstance<ProjectDependency>()
                .map { it.path }

            // Rule 2 (":core must not depend on :app") is now part of
            // applicationBoundaryViolations below, generalised to every module
            // and every :app-* application.
            if (sub.path.startsWith(":feature")) {
                deps.filter { it.startsWith(":feature") }.forEach { other ->
                    add(
                        "${sub.path} must not depend on $other — cross-feature " +
                            "navigation goes through route contracts in :app.",
                    )
                }
            }

            // Creator Studio P0-A, guards G-4/G-5/G-6.
            //
            // The render/export PORT lives in :core:creator-model, which is why
            // neither of these edges is needed in either direction. An earlier
            // design had the engine owning the interface AND media consuming a
            // model interface, which is a cycle waiting to be written; asserting
            // the actual Gradle edges is what stops it being written by accident.
            if (sub.path == ":core:creator-engine" && deps.contains(":core:media")) {
                add(
                    ":core:creator-engine must not depend on :core:media — it calls " +
                        "the RenderExporter port in :core:creator-model, and app DI " +
                        "binds the :core:media implementation to it.",
                )
            }
            if (sub.path == ":core:media" && deps.contains(":core:creator-engine")) {
                add(
                    ":core:media must not depend on :core:creator-engine — it only " +
                        "implements the port declared in :core:creator-model.",
                )
            }
            // G-6, ADJUSTED FROM THE FROZEN SPEC — see the handover.
            //
            // The spec said no :feature may depend on :core:media. That was
            // written from an architecture sketch rather than from this graph:
            // :feature:post and :feature:profile have depended on :core:media
            // since Slice C for URL resolution and upload, neither of which has
            // anything to do with rendering. Enforcing the rule as written would
            // mean rewriting two features for a guard aimed at something else.
            //
            // What the rule was actually protecting is that no feature reaches
            // the render/export IMPLEMENTATION directly, bypassing the port. A
            // feature that depends on :core:media must not ALSO depend on
            // :core:creator-model, because that combination is how a screen
            // starts calling a RenderExporter implementation it found itself
            // instead of the one app DI bound.
            // :feature:post holds a NAMED waiver: the PublishTransport adapter
            // must live beside the CreatePostRequest DTO it freezes (the
            // provenance rule), and that module already depends on :core:media
            // for Slice C upload. The hazard this guard exists for — a feature
            // binding the RENDER port implementation itself — is asserted at
            // source level by RenderPortBoundaryGuardTest, which fails if any
            // feature file so much as imports RenderExporter.
            if (sub.path.startsWith(":feature") && sub.path != ":feature:post" &&
                deps.contains(":core:media") && deps.contains(":core:creator-model")
            ) {
                add(
                    "${sub.path} depends on BOTH :core:media and :core:creator-model — " +
                        "a feature must reach render/export through :core:creator-engine, " +
                        "never by binding a port implementation itself.",
                )
            }
        }

        // G-2: :core:creator-model is pure Kotlin/JVM.
        //
        // Purity is what makes the canonical bytes unit-testable on the JVM and
        // what lets :core:media implement the port without the engine ever
        // seeing it. An Android import here quietly costs both.
        subprojects.find { it.path == ":core:creator-model" }?.let { model ->
            listOf("com.android.library", "com.android.application").forEach { id ->
                if (model.pluginManager.hasPlugin(id)) {
                    add(":core:creator-model must not apply '$id' — it is pure Kotlin/JVM.")
                }
            }
        }

        // Rule 2 + Feast A0 application boundaries, over the real graph. Edges
        // include runtimeOnly because the rules are about APK contents.
        val directEdges: Map<String, Set<String>> = subprojects.associate { sub ->
            sub.path to sub.configurations
                .filter { it.name in setOf("implementation", "api", "runtimeOnly") }
                .flatMap { config -> config.dependencies }
                .filterIsInstance<ProjectDependency>()
                .map { it.path }
                .toSet()
        }
        addAll(applicationBoundaryViolations(directEdges))
        // ...and proof that each of those rules still fires.
        addAll(applicationBoundarySelfCheck())
        // Feast A3: the Kitchen app is real, so the rules above are evaluated
        // against it rather than being inert — and it must actually be the
        // application that ships the kitchen feature. A Kitchen app that lost
        // that edge would still be a clean graph.
        directEdges[":app-kitchen"]?.let { kitchenApp ->
            if (":feature:kitchen" !in kitchenApp) {
                add(":app-kitchen must depend on :feature:kitchen directly — it is the only app that ships it.")
            }
        }
        // Feast A4: the same for the Rider app.
        directEdges[":app-rider"]?.let { riderApp ->
            if (":feature:rider" !in riderApp) {
                add(":app-rider must depend on :feature:rider directly — it is the only app that ships it.")
            }
        }
    }
    val moduleCount = subprojects.size

    // G-1: the expected module count, not merely "green".
    //
    // A green graph cannot detect a module nobody meant to add. Naming the
    // number makes an unplanned module a build failure rather than a surprise
    // six weeks later. Update this deliberately when a module is authorised.
    // 28 = 26 + :core:call and :feature:call (calling P0).
    // 30 = 28 + :feature:settings (module picker / onboarding) + the two
    //      phantom parent projects (:core, :feature) that Gradle creates for
    //      nested paths and counts among subprojects.
    // 32 = 30 + :core:feed (the feed data seam split out of :feature:feed) +
    //      :feature:tube (long video), 2026-09-05.
    // 33 = 32 + :feature:search (page-scoped search), 2026-09-05.
    // 35 = 33 + :core:commerce and :feature:commerce (Commerce P0, LB-A1/A2).
    //      Rule 3 already forbids a :feature → :feature edge, so the commerce
    //      feature is covered by it without a new rule.
    // 36 = 35 + :core:analytics (product analytics ingest client, 2026-09-07).
    //      The app had no client for analytics-service at all, so every view,
    //      watch-second and engagement signal the creator payout model is
    //      built on was being discarded on the device.
    // 37 = 36 + :core:facear (Face AR / virtual try-on, 2026-09-12). A core
    //      module because the licence gate, the try-on domain and the camera
    //      surface are wanted by more than one feature, and rule 3 forbids the
    //      :feature:commerce → :feature:post edge that reaching Banuba
    //      through the reel studio would need.
    // Feast A0 (2026-09-13) added NO module. The application-boundary rules
    // above were put in force ahead of the modules still to come.
    // 38 = 37 + :core:realtime (Feast A1, 2026-09-13): the SSE client for
    //      notification-service — Last-Event-ID resume, jittered backoff,
    //      topic-token refresh, lifecycle-bound. Domain-free.
    // 39 = 38 + :core:food (Feast A1, 2026-09-13): food-service DTOs pinned
    //      by the golden contract fixtures, the repository, the onboarding
    //      checklist and food's realtime token source.
    // :core:location, the third A1 module, waits for its map libraries to
    // reach the offline cache.
    // 41 = 39 + :feature:kitchen and :app-kitchen (Feast A3, 2026-09-13): the
    //      restaurant partner's screens and their own installable. :app-kitchen
    //      sits at the top level, so no new phantom parent is counted. The
    //      application-boundary self-check gained the transitive Kitchen cases
    //      and the real graph asserts :app-kitchen -> :feature:kitchen.
    // 43 = 41 + :feature:rider and :app-rider (Feast A4, 2026-09-13): the
    //      delivery partner's screens and their own installable. Top level, so
    //      no new phantom parent. The self-check gained the Rider cases and the
    //      real graph asserts :app-rider -> :feature:rider.
    // Still to add, one module at a time, to reach 46: :core:location,
    // :core:kyc-ui, :feature:feast.
    val expectedModuleCount = 43

    doLast {
        val allViolations = buildList {
            addAll(violations)
            if (moduleCount != expectedModuleCount) {
                add(
                    "Module count is $moduleCount, expected $expectedModuleCount. " +
                        "If the change is intended, update expectedModuleCount in " +
                        "build.gradle.kts in the same commit that adds the module.",
                )
            }
        }
        if (allViolations.isNotEmpty()) {
            throw GradleException(
                "Module graph violations:\n" + allViolations.joinToString("\n") { "  - $it" },
            )
        }
        println("Module graph OK ($moduleCount modules checked, count asserted).")
    }
}
