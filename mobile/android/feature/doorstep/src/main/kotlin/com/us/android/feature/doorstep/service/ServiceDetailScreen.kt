package com.us.android.feature.doorstep.service

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.RadioButtonDefaults
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorstep.catalogue.DuesBanner
import com.us.android.feature.doorstep.data.AddonGroupDto
import com.us.android.feature.doorstep.domain.GenderRules
import com.us.android.feature.doorstep.domain.SelectionRules
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.model.toRupeeText
import com.us.android.feature.doorstep.model.toShortRupeeText
import com.us.android.feature.doorstep.ui.BottomAction
import com.us.android.feature.doorstep.ui.ChoiceRow
import com.us.android.feature.doorstep.ui.DoorstepCard
import com.us.android.feature.doorstep.ui.DoorstepScreen
import com.us.android.feature.doorstep.ui.InfoNote
import com.us.android.feature.doorstep.ui.LoadingPane
import com.us.android.feature.doorstep.ui.MessagePane
import com.us.android.feature.doorstep.ui.PriceText
import com.us.android.feature.doorstep.ui.QuantityStepper
import com.us.android.feature.doorstep.ui.SectionLabel
import com.us.android.feature.doorstep.ui.Tone
import com.us.android.feature.doorstep.ui.durationText
import com.us.android.feature.doorstep.ui.listPadding

@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod")
fun ServiceDetailScreen(
    onBack: () -> Unit,
    onContinue: () -> Unit,
    onOpenOutstanding: () -> Unit,
    viewModel: ServiceDetailViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val service = state.service

    DoorstepScreen(
        title = service?.name ?: "Service",
        onBack = onBack,
        bottomBar = {
            if (service != null) {
                BottomAction(
                    label = "Choose address and time",
                    onClick = { if (viewModel.continueToBooking()) onContinue() },
                    enabled = !state.blocked,
                    summary = "${state.estimate.toRupeeText()} incl. GST · ${durationText(state.durationMinutes)}",
                )
            }
        },
    ) { padding ->
        when {
            state.loading && service == null -> LoadingPane()
            service == null -> MessagePane(
                title = "Couldn't load this service",
                body = state.error.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
            )
            else -> LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = listPadding(padding),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                item {
                    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                        if (service.description.isNotBlank()) {
                            Text(service.description, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                        }
                        InfoNote("Prices include GST. You pay in full when you book; no cash.")
                    }
                }
                if (state.blocked) {
                    item { DuesBanner(total = Paise(state.outstanding?.totalPaise ?: 0), onPay = onOpenOutstanding) }
                }
                if (service.inclusions.isNotEmpty() || service.exclusions.isNotEmpty()) {
                    item {
                        DoorstepCard {
                            service.inclusions.forEach { Text("✓ $it", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textSecondary) }
                            service.exclusions.forEach { Text("✕ $it", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted) }
                        }
                    }
                }

                item { SectionLabel("Choose an option") }
                items(service.options, key = { it.id }) { option ->
                    val selected = option.id == state.selection.optionId
                    ChoiceRow(selected = selected, onClick = { viewModel.selectOption(option.id) }, modifier = Modifier.fillMaxWidth()) {
                        RadioButton(
                            selected = selected,
                            onClick = { viewModel.selectOption(option.id) },
                            colors = RadioButtonDefaults.colors(selectedColor = UsTheme.extended.accentSolid),
                        )
                        Column(Modifier.weight(1f)) {
                            Text(option.name, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                            Text(durationText(option.durationMinutes), style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                        }
                        PriceText(price = Paise(option.pricePaise), mrp = option.mrpPaise?.let(::Paise))
                    }
                }
                SelectionRules.option(service, state.selection)?.takeIf { it.maxQuantity > 1 }?.let { option ->
                    item {
                        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
                            Text(
                                "Quantity (up to ${option.maxQuantity})",
                                style = MaterialTheme.typography.bodyMedium,
                                color = UsTheme.extended.textSecondary,
                                modifier = Modifier.weight(1f),
                            )
                            QuantityStepper(
                                quantity = state.selection.quantity,
                                onDecrease = { viewModel.setQuantity(state.selection.quantity - 1) },
                                onIncrease = { viewModel.setQuantity(state.selection.quantity + 1) },
                            )
                        }
                    }
                }

                service.addonGroups.forEach { group ->
                    item(key = "group-${group.id}") {
                        SectionLabel(group.name) {
                            Text(groupRuleText(group), style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textDim)
                        }
                        if (state.showViolations) {
                            state.violations.firstOrNull { it.groupId == group.id }?.let { InfoNote(it.message, tone = Tone.Danger) }
                        }
                    }
                    items(group.addons, key = { "addon-${it.id}" }) { addon ->
                        val checked = addon.id in state.selection.addonIds
                        ChoiceRow(selected = checked, onClick = { viewModel.toggleAddon(addon.id) }, modifier = Modifier.fillMaxWidth()) {
                            if (group.maxSelect == 1) {
                                RadioButton(
                                    selected = checked,
                                    onClick = { viewModel.toggleAddon(addon.id) },
                                    colors = RadioButtonDefaults.colors(selectedColor = UsTheme.extended.accentSolid),
                                )
                            } else {
                                Checkbox(
                                    checked = checked,
                                    onCheckedChange = { viewModel.toggleAddon(addon.id) },
                                    colors = CheckboxDefaults.colors(checkedColor = UsTheme.extended.accentSolid),
                                )
                            }
                            Column(Modifier.weight(1f)) {
                                Text(addon.name, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
                                if (addon.extraDurationMinutes > 0) {
                                    Text(
                                        "+${durationText(addon.extraDurationMinutes)}",
                                        style = MaterialTheme.typography.bodySmall,
                                        color = UsTheme.extended.textMuted,
                                    )
                                }
                            }
                            Text(
                                "+${Paise(addon.pricePaise).toShortRupeeText()}",
                                style = MaterialTheme.typography.bodyMedium,
                                fontWeight = FontWeight.SemiBold,
                                color = UsTheme.extended.textPrimary,
                            )
                        }
                    }
                }

                item { SectionLabel("Professional") }
                item {
                    val note = GenderRules.fixedRuleNote(service.category.genderRule)
                    DoorstepCard {
                        if (state.womanSwitchOffered) {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Column(Modifier.weight(1f)) {
                                    Text("Require a woman professional", style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                                    Text(
                                        "Only women professionals, verified through DigiLocker, will be offered this job. Fewer slots may be free.",
                                        style = MaterialTheme.typography.bodySmall,
                                        color = UsTheme.extended.textMuted,
                                    )
                                }
                                Spacer(Modifier.width(UsTheme.spacing.m))
                                Switch(
                                    checked = state.selection.requireWoman,
                                    onCheckedChange = viewModel::setRequireWoman,
                                    colors = SwitchDefaults.colors(
                                        checkedTrackColor = UsTheme.extended.accentSolid,
                                        checkedThumbColor = UsTheme.extended.onAccent,
                                    ),
                                )
                            }
                        } else if (note != null) {
                            InfoNote(note, tone = Tone.Accent)
                        }
                        Text(
                            "Background-checked professionals. Rework within ${service.reworkDays} days if something isn't right.",
                            style = MaterialTheme.typography.bodySmall,
                            color = UsTheme.extended.textMuted,
                        )
                    }
                }
            }
        }
    }
}

/** "Pick 1 · required", "Up to 2 · optional". */
private fun groupRuleText(group: AddonGroupDto): String {
    val need = SelectionRules.required(group)
    return when {
        need > 0 && need == group.maxSelect -> "Pick $need · required"
        need > 0 -> "Pick $need–${group.maxSelect} · required"
        else -> "Up to ${group.maxSelect} · optional"
    }
}
