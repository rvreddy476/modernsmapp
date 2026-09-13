package com.us.android.feature.kitchen.compliance

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.RadioButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsDatePickerField
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.network.ComplianceDto
import com.us.android.feature.kitchen.kyc.Gstin
import com.us.android.feature.kitchen.kyc.GstinCheck
import com.us.android.feature.kitchen.kyc.Pan
import com.us.android.feature.kitchen.kyc.PanCheck
import com.us.android.feature.kitchen.ui.CardHeading
import com.us.android.feature.kitchen.ui.FieldError
import com.us.android.feature.kitchen.ui.InfoNote
import com.us.android.feature.kitchen.ui.KitchenCard
import com.us.android.feature.kitchen.ui.KitchenScreen
import com.us.android.feature.kitchen.ui.LabeledValue
import com.us.android.feature.kitchen.ui.PillTone
import com.us.android.feature.kitchen.ui.SectionHeader

@Composable
fun ComplianceScreen(onBack: () -> Unit, viewModel: ComplianceViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    KitchenScreen(
        title = "Tax details",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(top = padding.calculateTopPadding() + 8.dp, bottom = padding.calculateBottomPadding() + 96.dp),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            state.saved?.let { SavedComplianceCard(it) }

            SectionHeader("Tax category")
            TaxCategoryOption.entries.forEach { option ->
                CategoryRow(option = option, selected = state.category == option, onClick = { viewModel.onCategory(option) })
            }
            FieldError(state.errors[ComplianceRules.FIELD_CATEGORY])
            InfoNote("Ask your tax adviser to confirm the category. It decides whether you need a GSTIN and who pays GST on your food.")

            SectionHeader("Business")
            UsTextField(
                value = state.legalName,
                onValueChange = viewModel::onLegalName,
                label = "Legal business name",
                errorText = state.errors[ComplianceRules.FIELD_LEGAL_NAME],
            )
            UsTextField(
                value = state.pan,
                onValueChange = viewModel::onPan,
                label = "PAN",
                placeholder = "ABCDE1234F",
                errorText = state.errors[ComplianceRules.FIELD_PAN],
            )
            (Pan.check(state.pan) as? PanCheck.Valid)?.let {
                if (state.errors[ComplianceRules.FIELD_PAN] == null) InfoNote("${it.holderType.label} PAN", tone = PillTone.Positive)
            }
            UsTextField(
                value = state.gstin,
                onValueChange = viewModel::onGstin,
                label = "GSTIN (if registered)",
                placeholder = "29ABCDE1234F1Z5",
                errorText = state.errors[ComplianceRules.FIELD_GSTIN],
            )
            (Gstin.check(state.gstin) as? GstinCheck.Valid)?.let {
                if (state.errors[ComplianceRules.FIELD_GSTIN] == null) {
                    InfoNote("${it.stateName} · the GSTIN's format and check character are right", tone = PillTone.Positive)
                }
            }
            if (state.category?.specifiedPremises == true) {
                UsDatePickerField(
                    value = state.declaredAt,
                    onValueChange = viewModel::onDeclaredAt,
                    label = "Specified premises declared on",
                    errorText = state.errors[ComplianceRules.FIELD_DECLARED_AT],
                    maxDate = viewModel.today,
                )
            }
            InfoNote("Your PAN is stored encrypted. Only its last characters are ever shown again.")
            UsButton(
                text = "Save tax details",
                onClick = viewModel::save,
                loading = state.saving,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun CategoryRow(option: TaxCategoryOption, selected: Boolean, onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(if (selected) UsTheme.extended.unreadRow else UsTheme.extended.bgCardSolid)
            .border(1.dp, if (selected) UsTheme.extended.accentSolid else UsTheme.extended.borderSubtle, shape)
            .selectable(selected = selected, onClick = onClick, role = Role.RadioButton)
            .padding(horizontal = 8.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RadioButton(
            selected = selected,
            onClick = null,
            colors = RadioButtonDefaults.colors(
                selectedColor = UsTheme.extended.accentSolid,
                unselectedColor = UsTheme.extended.textDim,
            ),
        )
        Column(modifier = Modifier.padding(start = 8.dp)) {
            Text(option.title, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
            Text(option.detail, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        }
    }
}

@Composable
private fun SavedComplianceCard(saved: ComplianceDto) {
    KitchenCard {
        CardHeading(title = "Tax details on file", titleColor = UsTheme.extended.statusSuccess)
        TaxCategoryOption.fromWire(saved.taxCategory)?.let { LabeledValue("Category", it.title) }
        if (saved.panMasked.isNotBlank()) LabeledValue("PAN", saved.panMasked)
        saved.gstin?.let { LabeledValue("GSTIN", it) }
        LabeledValue(
            label = "GST on your food",
            value = if (saved.gstLiability == "ECO_SECTION_9_5") "Paid by Feast (section 9(5))" else "Paid by your business",
        )
    }
}
