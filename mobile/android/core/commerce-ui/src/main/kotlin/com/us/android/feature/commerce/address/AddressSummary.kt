package com.us.android.feature.commerce.address

import com.us.android.core.commerce.model.Address

/** Renders an address as a single readable block. */
fun Address.summary(): String = listOfNotNull(
    contactName,
    line1,
    line2?.takeIf { it.isNotBlank() },
    landmark?.takeIf { it.isNotBlank() },
    "$city, $state $postalCode",
    phone,
).joinToString("\n")

/** A one-line form for compact contexts such as the checkout summary. */
fun Address.oneLine(): String =
    listOfNotNull(line1, city, postalCode).joinToString(", ")
