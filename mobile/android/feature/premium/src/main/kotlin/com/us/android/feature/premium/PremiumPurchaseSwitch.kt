package com.us.android.feature.premium

/**
 * The app-side switch for SELLING Premium on Android (founder, 13 Nov 2026).
 *
 * Google Play's payments policy for a digital pass bought in-app has not been
 * checked yet, so the purchase flow ships dark. `:app` provides this from the
 * build flag `BuildConfig.PREMIUM_PURCHASE_ENABLED`: true in debug builds
 * (every flavour, for the dev walk), false in every release build. Turning it
 * on for a release is a one-line change in `app/build.gradle.kts`, after the
 * policy check.
 *
 * Off, nothing can be bought: no "Get Premium" on Echo's sheet (which keeps
 * only OK), no passes on the screen, and no request to `plans` or `checkout`.
 * A viewer who already holds Premium or has an invoice (bought on the web)
 * still gets the Settings row and a screen with their status and invoices
 * (15 Nov 2026); everyone else gets no Settings row.
 */
data class PremiumPurchaseSwitch(val enabled: Boolean)
