package com.us.android.feature.commerce.seller

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.CategoryChoice
import com.us.android.core.commerce.model.NewProduct
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.TaxClass
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.feature.commerce.ui.describe
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.util.Locale
import java.util.UUID
import javax.inject.Inject

/**
 * A new listing.
 *
 * Prices are typed as rupees-and-paise text and converted ONCE, here, to
 * [Paise]. There is no `Double` anywhere on the path from the keyboard to the
 * database — the string is parsed straight to integer paise, so `1299.99`
 * becomes 129999 rather than passing through 1299.9899999999998.
 */
data class NewProductForm(
    val title: String = "",
    val description: String = "",
    /** As typed: "1299", "1299.5", "1299.99". */
    val sellingPrice: String = "",
    /** The struck-through price. Optional — many sellers do not run one. */
    val mrp: String = "",
    val openingStock: String = "",
    val taxClassId: String? = null,
    val taxClasses: List<TaxClass> = emptyList(),
    val loadingRates: Boolean = true,
    /**
     * Where the listing sits in browse (2026-09-30). Required at submit —
     * a product without one appears in no category — but NOT part of
     * [isComplete], so the button stays live and the refusal is said as
     * one line under it rather than as a button that never enables.
     */
    val categoryId: String? = null,
    val categories: List<CategoryChoice> = emptyList(),
    val loadingCategories: Boolean = true,
    val saving: Boolean = false,
    val error: String? = null,
) {
    val sellingPaise: Paise? get() = parseRupees(sellingPrice)
    val mrpPaise: Paise? get() = parseRupees(mrp)
    val stock: Int? get() = openingStock.trim().toIntOrNull()?.takeIf { it >= 0 }

    /** The chosen category's path, for the picker row. */
    val categoryLabel: String? get() = categories.firstOrNull { it.id == categoryId }?.label

    /**
     * Whether the struck-through price is a lie.
     *
     * An MRP at or below the selling price shows the buyer a "discount" that
     * is zero or negative. The server does not police this — [PriceLine] simply
     * stops striking through — but a seller who typed them the wrong way round
     * wants to know before the listing is live.
     */
    val mrpBelowSelling: Boolean
        get() {
            val m = mrpPaise ?: return false
            val s = sellingPaise ?: return false
            return m < s
        }

    val isComplete: Boolean
        get() = title.trim().length >= MIN_TITLE &&
            sellingPaise != null &&
            stock != null &&
            taxClassId != null &&
            !mrpBelowSelling
}

private const val MIN_TITLE = 3

/** The one line a create without a category gets. */
const val CHOOSE_A_CATEGORY = "Choose a category"

/**
 * Why the create is refused before it leaves the device, or null when it
 * may go. Pure, so the guard is a table test: today's only reason is a
 * missing category (2026-09-30).
 */
fun createRefusal(form: NewProductForm): String? =
    if (form.categoryId.isNullOrBlank()) CHOOSE_A_CATEGORY else null

/**
 * What a successful create hands the caller: the product, and the id of
 * its (single) VARIANT for the stock screen — never the product id, which
 * the stock route cannot look up. Null when no variant could be found, in
 * which case the caller goes back to the hub rather than to a screen that
 * would fail.
 */
data class CreatedProduct(
    val productId: String,
    val title: String,
    val variantId: String?,
)

/**
 * Parses rupees-and-paise text into integer paise.
 *
 * Deliberately not `toDouble() * 100`. That is the conversion this whole
 * migration exists to remove, and doing it in the one place a human types a
 * price would put the rounding error at the SOURCE, where every exact figure
 * downstream then faithfully preserves it.
 *
 * Returns null for anything that is not a positive amount with at most two
 * decimal places — including "1.239", which is not a price a seller can charge
 * and must not be silently rounded into one.
 */
internal fun parseRupees(raw: String): Paise? {
    val parts = raw.trim().takeIf { it.isNotEmpty() }?.split('.') ?: return null
    if (parts.size > MAX_PARTS) return null

    val rupees = parts[0].ifEmpty { "0" }
    if (!rupees.all(Char::isDigit)) return null

    val fraction = if (parts.size == 1) "00" else normaliseFraction(parts[1]) ?: return null

    val total = rupees.toLongOrNull()
        ?.let { it * PAISE_PER_RUPEE + fraction.toLong() }
        ?: return null
    return if (total > 0) Paise(total) else null
}

/** Pads "5" to "50" and refuses anything that is not at most two digits. */
private fun normaliseFraction(raw: String): String? =
    raw.takeIf { it.length <= PAISE_DIGITS && it.all(Char::isDigit) }
        ?.padEnd(PAISE_DIGITS, '0')

/** Renders paise back as the text a seller would type. */
internal fun Paise.asRupeeText(): String =
    String.format(Locale.ROOT, "%d.%02d", value / PAISE_PER_RUPEE, value % PAISE_PER_RUPEE)

private const val PAISE_PER_RUPEE = 100
private const val PAISE_DIGITS = 2
private const val MAX_PARTS = 2

@HiltViewModel
class NewProductViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _form = MutableStateFlow(NewProductForm())
    val form: StateFlow<NewProductForm> = _form.asStateFlow()

    init {
        loadRates()
        loadCategories()
    }

    /**
     * The GST rates.
     *
     * Loaded before the form can be submitted, because the class is required:
     * a product without one is unsellable, and the server refuses the create
     * rather than letting a seller list something no buyer can complete.
     */
    fun loadRates() {
        _form.value = _form.value.copy(loadingRates = true, error = null)
        viewModelScope.launch {
            when (val r = repo.taxClasses()) {
                is CommerceResult.Failure -> _form.value = _form.value.copy(
                    loadingRates = false,
                    error = r.error.describe(),
                )

                is CommerceResult.Success -> _form.value = _form.value.copy(
                    taxClasses = r.value,
                    loadingRates = false,
                    // Never preselected. Picking one for the seller files the
                    // wrong tax on every sale of anything that is not that
                    // rate, and it is the kind of default nobody revisits.
                    taxClassId = null,
                )
            }
        }
    }

    /**
     * The categories a listing may sit under: the tree's listable leaves,
     * labelled by path. Loaded beside the rates rather than gating on them,
     * so a slow taxonomy does not hold the whole form. Never preselected:
     * a listing filed under the wrong heading is one buyers never find.
     */
    fun loadCategories() {
        _form.value = _form.value.copy(loadingCategories = true)
        viewModelScope.launch {
            val choices = when (val r = repo.listableCategories()) {
                is CommerceResult.Success -> r.value
                is CommerceResult.Failure -> emptyList()
            }
            _form.value = _form.value.copy(categories = choices, loadingCategories = false)
        }
    }

    fun update(transform: (NewProductForm) -> NewProductForm) {
        _form.value = transform(_form.value).copy(error = null)
    }

    fun submit(onCreated: (CreatedProduct) -> Unit) {
        val form = _form.value
        val selling = form.sellingPaise ?: return
        val stock = form.stock ?: return
        val taxClassId = form.taxClassId ?: return
        if (!form.isComplete || form.saving) return
        // Refused here, as one line, rather than by a disabled button: with
        // five fields on the form a seller cannot tell which one is holding
        // the button, and the category is the one they most often skip.
        createRefusal(form)?.let { refusal ->
            _form.value = form.copy(error = refusal)
            return
        }

        _form.value = form.copy(saving = true, error = null)
        viewModelScope.launch {
            val product = NewProduct(
                title = form.title.trim(),
                description = form.description.trim().takeIf { it.isNotBlank() },
                taxClassId = taxClassId,
                categoryId = form.categoryId,
                // The SKU is an internal identifier the buyer never sees, and
                // asking for one on a first listing is a question most sellers
                // cannot answer. Generated, not demanded.
                sku = "SKU-" + UUID.randomUUID().toString().take(SKU_CHARS).uppercase(Locale.ROOT),
                // No MRP means no struck-through price, so it equals the
                // selling price rather than zero — a zero MRP renders as a
                // 100% discount.
                mrp = form.mrpPaise ?: selling,
                sellingPrice = selling,
                openingStock = stock,
            )
            when (val r = repo.createProduct(product)) {
                is CommerceResult.Failure ->
                    _form.value = form.copy(saving = false, error = r.error.describe())

                is CommerceResult.Success -> {
                    // The stock screen needs the VARIANT's id (2026-09-30).
                    // `POST /products` answers the product row, which today
                    // carries no variants, so the repository reads them; a
                    // server that does include them costs no second call.
                    val created = r.value
                    val variantId = when (val v = repo.variantsForEdit(created)) {
                        is CommerceResult.Success -> v.value.firstOrNull()?.id
                        is CommerceResult.Failure -> null
                    }
                    _form.value = form.copy(saving = false)
                    onCreated(CreatedProduct(productId = created.id, title = created.title, variantId = variantId))
                }
            }
        }
    }

    private companion object {
        const val SKU_CHARS = 8
    }
}
