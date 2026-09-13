package com.us.android.feature.kitchen

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.Paise
import com.us.android.feature.kitchen.compliance.ComplianceRules
import com.us.android.feature.kitchen.compliance.TaxCategoryOption
import com.us.android.feature.kitchen.hours.DayHours
import com.us.android.feature.kitchen.hours.OperatingHoursRules
import com.us.android.feature.kitchen.kycui.BankAccountFormRules
import com.us.android.feature.kitchen.kycui.BankAccountFormState
import com.us.android.feature.kitchen.kycui.BankField
import com.us.android.feature.kitchen.menu.MenuItemRules
import org.junit.Test
import java.math.BigDecimal
import java.time.LocalDate

/** The step forms' pure rules: what blocks a save before a round trip. */
class FormRulesTest {

    private val today = LocalDate.parse("2026-09-13")

    @Test
    fun `compliance refuses a GSTIN whose embedded PAN is not the PAN given`() {
        val errors = ComplianceRules.validate(
            category = TaxCategoryOption.RESTAURANT_STANDALONE,
            legalName = "Test Kitchens",
            pan = "ZZZPZ0000Z",
            gstin = "29ZZZCZ0000Z1Z" + "1", // any 15th char; the PAN mismatch or checksum must refuse it
            declaredAt = "",
            today = today,
        )
        assertThat(errors).containsKey(ComplianceRules.FIELD_GSTIN)

        val matching = ComplianceRules.validate(
            category = TaxCategoryOption.RESTAURANT_STANDALONE,
            legalName = "Test Kitchens",
            pan = "zzzpz0000z",
            gstin = "29ZZZPZ0000Z1Z6",
            declaredAt = "",
            today = today,
        )
        assertThat(matching).isEmpty()
    }

    @Test
    fun `specified premises need a declaration date that is not in the future`() {
        fun errorsFor(date: String) = ComplianceRules.validate(
            TaxCategoryOption.RESTAURANT_SPECIFIED_PREMISES, "Test", "ZZZPZ0000Z", "", date, today,
        )
        assertThat(errorsFor("")).containsKey(ComplianceRules.FIELD_DECLARED_AT)
        assertThat(errorsFor("2026-09-14")).containsKey(ComplianceRules.FIELD_DECLARED_AT)
        assertThat(errorsFor("2026-09-13")).isEmpty()
        assertThat(ComplianceRules.validate(null, "", "", "", "", today).keys)
            .containsAtLeast(ComplianceRules.FIELD_CATEGORY, ComplianceRules.FIELD_LEGAL_NAME, ComplianceRules.FIELD_PAN)
    }

    @Test
    fun `the bank form needs matching account numbers and a well-formed IFSC`() {
        val good = BankAccountFormState("Test Kitchens", "123456789012", "123456789012", "hdfc0000053")
        assertThat(BankAccountFormRules.validate(good)).isEmpty()

        val bad = BankAccountFormRules.validate(BankAccountFormState("", "12345", "12345", "HDFC1000053"))
        assertThat(bad.keys).containsExactly(BankField.HOLDER, BankField.ACCOUNT, BankField.IFSC)

        val mismatch = BankAccountFormRules.validate(good.copy(confirmAccountNumber = "123456789013"))
        assertThat(mismatch.keys).containsExactly(BankField.CONFIRM)
        assertThat(BankAccountFormRules.fieldFor("ifsc")).isEqualTo(BankField.IFSC)
    }

    @Test
    fun `opening hours normalise typed times and allow overnight windows`() {
        assertThat(OperatingHoursRules.normalizeTime("9:30")).isEqualTo("09:30")
        assertThat(OperatingHoursRules.normalizeTime("0930")).isEqualTo("09:30")
        assertThat(OperatingHoursRules.normalizeTime(" 23:59 ")).isEqualTo("23:59")

        val overnight = DayHours(5, open = true, opensAt = "18:00", closesAt = "02:00")
        assertThat(OperatingHoursRules.errorFor(overnight)).isNull()
        assertThat(OperatingHoursRules.isOvernight(overnight)).isTrue()
        assertThat(OperatingHoursRules.errorFor(DayHours(1, true, "24:00", "02:00"))).isNotNull()
        assertThat(OperatingHoursRules.errorFor(DayHours(1, true, "10:00", "10:00"))).isNotNull()
    }

    @Test
    fun `the hours request carries the whole week Sunday first with closed days empty`() {
        val week = OperatingHoursRules.defaultWeek().map { if (it.dayOfWeek == 0) it.copy(open = false) else it }
        val windows = OperatingHoursRules.toRequest(week.shuffled()).windows

        assertThat(windows.map { it.dayOfWeek }).containsExactly(0, 1, 2, 3, 4, 5, 6).inOrder()
        assertThat(windows.first().isClosed).isTrue()
        assertThat(windows.first().opensAt).isEmpty()
        assertThat(OperatingHoursRules.dayForServerField("windows[3].opens_at", week)).isEqualTo(3)
        assertThat(OperatingHoursRules.validate(week.map { it.copy(open = false) }))
            .containsKey(OperatingHoursRules.ALL_CLOSED)
    }

    @Test
    fun `dish prices parse to paise and an offer must undercut the price`() {
        val (errors, parsed) = MenuItemRules.validate("Masala dosa", "129.50", "99", "15", "5")
        assertThat(errors).isEmpty()
        assertThat(parsed!!.basePrice).isEqualTo(Paise(12_950))
        assertThat(parsed.offerPrice).isEqualTo(Paise(9_900))
        assertThat(parsed.taxPercent).isEqualTo(BigDecimal("5"))

        val (offerErrors, _) = MenuItemRules.validate("Dosa", "99", "129", "15", "5")
        assertThat(offerErrors).containsKey(MenuItemRules.FIELD_OFFER)

        val (manyErrors, none) = MenuItemRules.validate("", "12.345", "", "0", "40")
        assertThat(manyErrors.keys).containsExactly(
            MenuItemRules.FIELD_NAME, MenuItemRules.FIELD_PRICE, MenuItemRules.FIELD_PREP, MenuItemRules.FIELD_TAX,
        )
        assertThat(none).isNull()
    }
}
