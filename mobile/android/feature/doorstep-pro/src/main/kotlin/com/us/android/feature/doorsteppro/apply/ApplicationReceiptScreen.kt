package com.us.android.feature.doorsteppro.apply

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.*
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.ProfessionalDto
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter

@Composable
fun ApplicationReceiptScreen(professional: ProfessionalDto, refreshing: Boolean, onRefresh: () -> Unit) {
    var contactOpen by remember { mutableStateOf(false) }
    if (contactOpen) { AlternateContactScreen(onBack = { contactOpen = false }); return }
    val application = professional.application
    val submitted = application?.submittedAt?.let { raw ->
        runCatching { OffsetDateTime.parse(raw).atZoneSameInstant(ZoneId.of("Asia/Kolkata")).format(DateTimeFormatter.ofPattern("d MMM yyyy")) }.getOrNull()
    }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl)) {
        UsFeatureBanner(
            title = "Application under review", description = "We'll update your status here when the review is complete.",
            eyebrow = professional.displayName, icon = UsIcons.FileText,
        )
        UsSurfaceCard(Modifier.fillMaxWidth()) {
            Text("Application progress", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            if (application != null) {
                Text("Your services, identity and bank details were submitted together.", color = UsTheme.extended.textSecondary)
                ReceiptStep("Identity documents", "Submitted for review", submitted, complete = true)
                ReceiptStep("Services information", "Business and service details submitted", submitted, complete = true)
                ReceiptStep("Bank details", "Payout details submitted", submitted, complete = true)
            }
            ReceiptStep("Verification in progress", "Our team is reviewing your application.", null, complete = false)
            // Submission is not approval; never invent review dates or email delivery promises.
            Text("No need to submit again while your application is under review.", color = UsTheme.extended.textSecondary, style = MaterialTheme.typography.bodyMedium)
            UsButton("Refresh status", onRefresh, Modifier.fillMaxWidth(), loading = refreshing)
        }
        UsSurfaceCard(Modifier.fillMaxWidth()) {
            Text("Application details", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
            Text(professional.displayName)
            application?.let { Text("${it.town}, ${it.stateName}", color = UsTheme.extended.textSecondary) }
            UsSecondaryButton("Alternate contact", { contactOpen = true }, Modifier.fillMaxWidth())
        }
        Spacer(Modifier.height(UsTheme.spacing.l))
    }
}

@Composable
private fun ReceiptStep(title: String, subtitle: String, date: String?, complete: Boolean) {
    Row(Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.m), horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l), verticalAlignment = Alignment.Top) {
        Icon(if (complete) UsIcons.Check else UsIcons.Clock, null,
            Modifier.size(24.dp), tint = if (complete) UsTheme.extended.statusSuccess else UsTheme.extended.statusWarning)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(title, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            Text(subtitle, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
            date?.let { Text(it, style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textMuted) }
        }
    }
}
