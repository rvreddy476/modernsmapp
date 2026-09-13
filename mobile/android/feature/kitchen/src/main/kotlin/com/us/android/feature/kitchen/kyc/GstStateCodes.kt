package com.us.android.feature.kitchen.kyc

/**
 * The GST state and union-territory code table — the first two characters of
 * every GSTIN. A copy of shared/kyc `gstStateNames`, entry for entry.
 *
 * 01..38 and 97 are assigned. 99 (Centre Jurisdiction) is refused, as the
 * server refuses it. 25 and 28 are kept because GSTINs issued under them may
 * still be on record.
 */
object GstStateCodes {

    private val names: Map<String, String> = linkedMapOf(
        "01" to "Jammu and Kashmir",
        "02" to "Himachal Pradesh",
        "03" to "Punjab",
        "04" to "Chandigarh",
        "05" to "Uttarakhand",
        "06" to "Haryana",
        "07" to "Delhi",
        "08" to "Rajasthan",
        "09" to "Uttar Pradesh",
        "10" to "Bihar",
        "11" to "Sikkim",
        "12" to "Arunachal Pradesh",
        "13" to "Nagaland",
        "14" to "Manipur",
        "15" to "Mizoram",
        "16" to "Tripura",
        "17" to "Meghalaya",
        "18" to "Assam",
        "19" to "West Bengal",
        "20" to "Jharkhand",
        "21" to "Odisha",
        "22" to "Chhattisgarh",
        "23" to "Madhya Pradesh",
        "24" to "Gujarat",
        "25" to "Daman and Diu",
        "26" to "Dadra and Nagar Haveli and Daman and Diu",
        "27" to "Maharashtra",
        "28" to "Andhra Pradesh (before reorganisation)",
        "29" to "Karnataka",
        "30" to "Goa",
        "31" to "Lakshadweep",
        "32" to "Kerala",
        "33" to "Tamil Nadu",
        "34" to "Puducherry",
        "35" to "Andaman and Nicobar Islands",
        "36" to "Telangana",
        "37" to "Andhra Pradesh",
        "38" to "Ladakh",
        "97" to "Other Territory",
    )

    /** The name for an exact two-character code; not trimmed, like `GSTStateName`. */
    fun nameOf(code: String): String? = names[code]

    fun isAssigned(code: String): Boolean = code in names

    /**
     * The names `PUT …/location` accepts for `state` — food-service's
     * `KnownStateNames`: every table name except 97 (Other Territory) and 28
     * (pre-reorganisation Andhra Pradesh). Sorted for the picker.
     */
    val locationStateNames: List<String> =
        names.filterKeys { it != OTHER_TERRITORY && it != ANDHRA_PRE_REORGANISATION }.values.sorted()

    private const val OTHER_TERRITORY = "97"
    private const val ANDHRA_PRE_REORGANISATION = "28"
}
