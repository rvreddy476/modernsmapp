package com.us.android.core.commerce.repository

import com.us.android.core.commerce.network.TryOnDto
import com.us.android.core.facear.TryOnDescriptor
import com.us.android.core.facear.TryOnKind
import com.us.android.core.facear.TryOnVariant

/**
 * `try_on` on the wire → the try-on domain, or null.
 *
 * Null for every case that is not a try-on this build can actually run, so no
 * screen downstream has to re-litigate it:
 *
 *  * no `try_on` object (an older server) — the field is already null here;
 *  * `capable: false` — the seller switched it off;
 *  * a `kind` this build has not seen — the effect could not be parameterised
 *    and guessing would put the wrong thing on someone's face;
 *  * a blank `effect_slug` — nothing to load.
 *
 * Variants are filtered rather than the whole descriptor rejected: a shade
 * list with one bad row is still a usable try-on, and a variant with no id
 * cannot be selected or sent to the effect anyway. A `capable` product whose
 * every variant is unusable still resolves — the effect's own default shade is
 * a legitimate try-on.
 */
fun toTryOnDescriptor(dto: TryOnDto?): TryOnDescriptor? {
    val source = dto ?: return null
    if (!source.capable) return null
    val kind = TryOnKind.from(source.kind)
    val slug = source.effectSlug.trim()
    if (kind == TryOnKind.UNKNOWN || slug.isBlank()) return null
    return TryOnDescriptor(
        capable = true,
        kind = kind,
        effectSlug = slug,
        variants = source.variants
            .filter { it.id.isNotBlank() }
            .map { variant ->
                TryOnVariant(
                    id = variant.id,
                    // A swatch with no words is unreadable by a screen reader
                    // and ambiguous to everyone else; the id is a poor label
                    // but it is a label.
                    label = variant.label.ifBlank { variant.id },
                    hex = variant.hex?.takeIf { it.isNotBlank() },
                    // The effect-specific call, when the seller supplied one.
                    // Blank is not a script; it would make the JS-call builder
                    // return an empty `evalJs` instead of the generated call.
                    js = variant.js?.takeIf { it.isNotBlank() },
                )
            },
    )
}
