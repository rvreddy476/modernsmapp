package com.us.doorstep.pro.partner

import android.content.Intent
import android.provider.Settings
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.us.android.core.designsystem.component.*
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.root.ProRoot
import com.us.android.feature.doorsteppro.root.ProRootViewModel
import com.us.android.feature.rider.root.RiderRoot
import com.us.android.feature.rider.root.RiderRootViewModel
import com.us.android.feature.mopedu.captain.MopeduCaptainRoute
import com.us.android.feature.mopedu.captain.MopeduCaptainViewModel
import com.us.android.feature.mopedu.captain.payment.CaptainPaymentRequest

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PartnerWorkspace(
    userId: String,
    requestedBusiness: PartnerBusiness?,
    onRequestConsumed: () -> Unit,
    onOpenPayment: (CaptainPaymentRequest) -> Unit,
    onAbandonPayment: (CaptainPaymentRequest) -> Unit,
    viewModel: PartnerWorkspaceViewModel = hiltViewModel(key = "workspace-$userId"),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var tab by rememberSaveable { mutableIntStateOf(0) }
    var activeName by rememberSaveable { mutableStateOf<String?>(null) }
    var chooseBusinesses by rememberSaveable { mutableStateOf(false) }
    var accountMenu by remember { mutableStateOf(false) }
    var confirmSignOut by remember { mutableStateOf(false) }
    val active = PartnerBusiness.entries.firstOrNull { it.name == activeName && it.available }
    val holder = rememberSaveableStateHolder()
    val context = LocalContext.current
    val name = state.summaries.firstOrNull { it.business == PartnerBusiness.HOME }?.name.orEmpty()
        .ifBlank { state.summaries.firstOrNull { it.name.isNotBlank() }?.name.orEmpty() }
    fun closeBusiness() { activeName = null; viewModel.refresh() }
    fun selectTab(index: Int) { closeBusiness(); tab = index; accountMenu = false }
    fun notificationSettings() {
        context.startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName))
    }
    LaunchedEffect(userId) { viewModel.initialise(userId) }
    LifecycleResumeEffect(userId) {
        viewModel.initialise(userId); viewModel.refresh(); viewModel.setVisible(true)
        onPauseOrDispose { viewModel.setVisible(false) }
    }
    LaunchedEffect(requestedBusiness) {
        requestedBusiness?.let { if (it.available) { activeName = it.name; tab = 0 }; onRequestConsumed() }
    }
    LaunchedEffect(tab) { if (tab == 2) viewModel.loadAlerts() }
    BackHandler(active != null) { closeBusiness() }
    PartnerShell(
        title = active?.title ?: "Cleestudio Partner", name = name,
        refreshing = state.refreshing, selectedTab = if (active != null) 0 else tab,
        unread = state.alerts.any { !it.isRead }, onTab = ::selectTab,
        onRefresh = viewModel::refresh, onAccount = { accountMenu = true },
        onBack = if (active != null) ::closeBusiness else null,
    ) {
        if (active != null) {
            if (state.summaries.any { it.business == active && it.status == BusinessStatus.UNAVAILABLE }) {
                UnavailableBusiness(active, state.refreshing, viewModel::refresh)
            } else holder.SaveableStateProvider(active.name) {
                PartnerBusinessWorkspace(active, userId, viewModel, onOpenPayment, onAbandonPayment) { confirmSignOut = true }
            }
        } else if (tab <= 1) {
            PartnerBusinessOverview(state, name, showGreeting = tab == 0,
                onAdd = { chooseBusinesses = true }, onOpen = { activeName = it.name }, onRefresh = viewModel::refresh,
                onOpenWork = { activeName = viewModel.openWork(it).name })
        } else {
            LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(UsTheme.spacing.pageHorizontal),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                if (tab == 2) {
                    item {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Text("Notifications", Modifier.weight(1f), style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                            IconButton(viewModel::loadAlerts, enabled = !state.alertsLoading) { Icon(UsIcons.RotateCw, "Refresh notifications") }
                        }
                        if (state.alertsLoading) LinearProgressIndicator(Modifier.fillMaxWidth())
                    }
                    if (state.alertsError) item { Text("Couldn't load notifications. Please retry.", color = UsTheme.extended.statusDanger) }
                    else if (!state.alertsLoading && state.alerts.isEmpty()) item { Text("No new notifications.", color = UsTheme.extended.textSecondary) }
                    items(state.alerts, key = { it.notificationId.ifBlank { "${it.bucket}:${it.ts}" } }) { alert ->
                        ActionCard(partnerAlertTitle(alert.type), (if (!alert.isRead) "Unread · " else "") + PartnerBusiness.forNotification(alert.type)?.title.orEmpty(), UsIcons.Notifications) {
                            activeName = viewModel.openAlert(alert)?.name
                        }
                    }
                } else {
                    item { Text(name.ifBlank { "Your account" }, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold) }
                    item { ActionCard("Profile & documents", "Select your work category", UsIcons.FileText) { selectTab(1) } }
                    item { ActionCard("Notification settings", "Alerts on this phone", UsIcons.Notifications, ::notificationSettings) }
                    item { UsSecondaryButton("Sign out", { confirmSignOut = true }) }
                }
            }
        }
    }
    if (accountMenu) ModalBottomSheet(onDismissRequest = { accountMenu = false }, containerColor = UsTheme.extended.bgSheet) {
        Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(UsTheme.spacing.pageHorizontal)) {
            Text(name.ifBlank { "Partner account" }, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            AccountMenuItem("Your account", UsIcons.Profile) { selectTab(3) }
            AccountMenuItem("Switch business", UsIcons.Repost) { selectTab(1) }
            AccountMenuItem("Profile & documents", UsIcons.FileText) { selectTab(1) }
            AccountMenuItem("Notifications", UsIcons.Notifications) { selectTab(2) }
            AccountMenuItem("Notification settings", UsIcons.Settings) { accountMenu = false; notificationSettings() }
            HorizontalDivider(color = UsTheme.extended.borderSubtle)
            TextButton({ accountMenu = false; confirmSignOut = true }, Modifier.fillMaxWidth()) { Text("Sign out", color = UsTheme.extended.statusDanger) }
        }
    }
    if (chooseBusinesses) {
        var chosen by remember { mutableStateOf(state.selected) }
        ModalBottomSheet(onDismissRequest = { chooseBusinesses = false }, containerColor = UsTheme.extended.bgSheet) {
            Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(UsTheme.spacing.pageHorizontal),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                Text("Choose your work", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                Text("Select one or more categories.", color = UsTheme.extended.textSecondary)
                PartnerBusiness.entries.forEach { role ->
                    val summary = state.summaries.firstOrNull { it.business == role }
                    val registered = summary?.registered == true
                    val available = role.available && summary?.status != BusinessStatus.UNAVAILABLE
                    Row(Modifier.fillMaxWidth().heightIn(min = 64.dp), verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(role in chosen || registered, enabled = available && !registered,
                            onCheckedChange = { checked -> chosen = if (checked) chosen + role else chosen - role })
                        Column(Modifier.weight(1f)) {
                            Text(role.title, style = MaterialTheme.typography.titleSmall)
                            Text(if (!available) "Not available yet" else if (registered) "Already added" else role.description,
                                style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textSecondary)
                        }
                    }
                }
                UsButton("Done", { viewModel.saveBusinesses(chosen); chooseBusinesses = false; selectTab(1) })
                Spacer(Modifier.height(UsTheme.spacing.m))
            }
        }
    }
    if (confirmSignOut) AlertDialog(onDismissRequest = { confirmSignOut = false }, title = { Text("Sign out?") },
        confirmButton = { TextButton({ confirmSignOut = false; viewModel.signOut() }) { Text("Sign out") } },
        dismissButton = { TextButton({ confirmSignOut = false }) { Text("Cancel") } })
    PartnerRequestsHost(userId, state.summaries)
}

/** Shared by the actual app and native layout tests, including both fixed bars. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun PartnerShell(title: String, name: String, refreshing: Boolean, selectedTab: Int, unread: Boolean,
    onTab: (Int) -> Unit, onRefresh: () -> Unit, onAccount: () -> Unit, onBack: (() -> Unit)? = null,
    content: @Composable () -> Unit) {
    UsScaffold(applyPageGutter = false, topBar = {
        TopAppBar(title = { Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold) },
            navigationIcon = { onBack?.let { IconButton(it) { Icon(UsIcons.Back, "Back to businesses") } } },
            actions = {
                if (onBack == null) IconButton(onRefresh, enabled = !refreshing) { Icon(UsIcons.RotateCw, "Refresh businesses") }
                IconButton(onAccount) { UsAvatar(name.ifBlank { "Partner" }, size = UsAvatarSize.Small, contentDescription = "Open account menu") }
            }, colors = TopAppBarDefaults.topAppBarColors(containerColor = UsTheme.extended.bgCardSolid))
    }, bottomBar = {
        UsNavigationBar(listOf(UsNavItem("Home", UsIcons.Home), UsNavItem("Work", UsIcons.Store),
            UsNavItem("Alerts", UsIcons.Notifications, if (unread) "Alerts, unread notifications" else "Alerts"),
            UsNavItem("Account", UsIcons.Profile)), selectedTab, onTab)
    }) { insets ->
        Box(Modifier.fillMaxSize().padding(insets).consumeWindowInsets(insets), contentAlignment = Alignment.TopCenter) {
            Box(Modifier.widthIn(max = 720.dp).fillMaxSize()) { content() }
        }
    }
}

@Composable
internal fun PartnerBusinessOverview(state: WorkspaceState, name: String, showGreeting: Boolean,
    onAdd: () -> Unit, onOpen: (PartnerBusiness) -> Unit, onRefresh: () -> Unit,
    onOpenWork: (PartnerReservation) -> Unit = { onOpen(it.business) }) {
    BoxWithConstraints(Modifier.fillMaxSize()) {
        val columns = businessColumnCount(maxWidth.value, LocalDensity.current.fontScale)
        val visible = PartnerBusiness.entries.map { business ->
            state.summaries.firstOrNull { it.business == business } ?: BusinessSummary(business,
                if (state.refreshing) BusinessStatus.LOADING else BusinessStatus.UNKNOWN)
        }.filter { !showGreeting || it.registered || it.business in state.selected }
        // One bounded list, no nested scrolling grid, repeated feed or full-screen cards.
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            if (showGreeting) item {
                Column(Modifier.padding(bottom = UsTheme.spacing.xs)) {
                    Text("Welcome back", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
                    Text(name.ifBlank { "Ready for your next job?" }, style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
                }
            }
            if (showGreeting) {
                item {
                    UsSurfaceCard(Modifier.fillMaxWidth()) {
                        Text("Duty & current work", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
                        if (state.workLoading) Text("Checking your work status…")
                        else if (state.workError) {
                            Text("Couldn't refresh duty and current work. Open your work category to check before taking another job.", color = UsTheme.extended.statusWarning)
                            TextButton(onRefresh) { Text("Check again") }
                        } else Text(state.workSession?.category?.let { "On duty · ${it.title}" } ?: "You are off duty", color = UsTheme.extended.textSecondary)
                        Text("One category can be on duty at a time. Accepted work and scheduled visits stay reserved.", style = MaterialTheme.typography.bodySmall)
                    }
                }
                state.workSession?.reservations.orEmpty().filter { it.state != "offered" || it.offerExpiresAt?.isAfter(java.time.Instant.now()) == true }.forEach { work -> item(key = "work-${work.business}-${work.jobId}") {
                    ActionCard(work.business.title, when (work.state) {
                        "active" -> "Current job · open details"
                        "offered" -> "Request waiting · check time remaining"
                        else -> "Reserved · ${java.time.format.DateTimeFormatter.ofPattern("d MMM, h:mm a").withZone(java.time.ZoneId.systemDefault()).format(work.startsAt)}"
                    }, UsIcons.Store) { onOpenWork(work) }
                } }
            }
            item {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text(if (showGreeting) "Your work" else "Work categories", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
                    TextButton(onAdd) { Icon(UsIcons.Create, null, Modifier.size(18.dp)); Spacer(Modifier.width(UsTheme.spacing.xs)); Text("Add") }
                }
                if (state.refreshing) LinearProgressIndicator(Modifier.fillMaxWidth())
            }
            items(visible.chunked(columns), key = { row -> row.joinToString { it.business.name } }) { row ->
                Row(Modifier.fillMaxWidth().height(IntrinsicSize.Min), horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                    row.forEach { summary -> BusinessCard(summary, Modifier.weight(1f).fillMaxHeight()) { onOpen(summary.business) } }
                    repeat(columns - row.size) { Spacer(Modifier.weight(1f)) }
                }
            }
            if (visible.isEmpty() && !state.refreshing) item {
                Text("Add a work category to get started.", color = UsTheme.extended.textSecondary)
            }
            val failed = visible.filter { it.status == BusinessStatus.UNKNOWN }
            if (failed.isNotEmpty()) item {
                Text("Couldn't check ${failed.joinToString { it.business.title }}.", style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.statusWarning)
                TextButton(onRefresh, enabled = !state.refreshing) { Text("Retry") }
            }
        }
    }
}

@Composable
private fun PartnerBusinessWorkspace(business: PartnerBusiness, userId: String, workspace: PartnerWorkspaceViewModel,
    onOpenPayment: (CaptainPaymentRequest) -> Unit, onAbandonPayment: (CaptainPaymentRequest) -> Unit, onSignOut: () -> Unit) {
    val nav = rememberNavController()
    var introduction by rememberSaveable { mutableStateOf(false) }
    var foodSupport by rememberSaveable { mutableStateOf(false) }
    val workspaceState by workspace.state.collectAsStateWithLifecycle()
    if (introduction) {
        PartnerIntroduction(business, { introduction = false }, hiltViewModel(key = "intro-${business.name}-$userId"))
        return
    }
    if (foodSupport) {
        FoodDeliverySupport({ foodSupport = false }, hiltViewModel(key = "food-support-$userId"))
        return
    }
    Column(Modifier.fillMaxSize()) {
      if (business == PartnerBusiness.FOOD_DELIVERY) TextButton({ foodSupport = true }, Modifier.align(Alignment.End)) { Text("Delivery support") }
      if (business in setOf(PartnerBusiness.MOPED, PartnerBusiness.FOOD_DELIVERY) && workspaceState.summaries.any { it.business == business && it.registered }) {
          TextButton({ introduction = true }, Modifier.align(Alignment.End)) { Text("Introduction (optional)") }
      }
    NavHost(nav, startDestination = business.name, modifier = Modifier.weight(1f)) {
        composable(business.name) {
            when (business) {
                PartnerBusiness.HOME -> ProRoot(hiltViewModel<ProRootViewModel>(key = "home-$userId"), onProfessionalChanged = {
                    workspace.syncHomeStatus(it.id, it.displayName, it.status)
                }, incomingPopups = false)
                PartnerBusiness.FOOD_DELIVERY -> RiderRoot(hiltViewModel<RiderRootViewModel>(key = "delivery-$userId"))
                PartnerBusiness.MOPED -> MopeduCaptainRoute(onSignOut, onOpenPayment, onAbandonPayment,
                    viewModel = hiltViewModel<MopeduCaptainViewModel>(key = "ride-$userId"))
                PartnerBusiness.COMMERCE_DELIVERY -> CommerceDeliveryWorkspace(hiltViewModel(key = "commerce-delivery-$userId"))
            }
        }
    }
    }
}

internal fun businessColumnCount(widthDp: Float, fontScale: Float): Int = if (widthDp >= 340f && fontScale <= 1.2f) 2 else 1

@Composable
internal fun UnavailableBusiness(business: PartnerBusiness, refreshing: Boolean, onRefresh: () -> Unit) {
    Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(UsTheme.spacing.pageHorizontal)) {
        UsSurfaceCard(Modifier.fillMaxWidth()) {
            Text("${business.title} is currently unavailable", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
            Text("You can continue using your other work categories.", color = UsTheme.extended.textSecondary)
            UsSecondaryButton("Check again", onRefresh, enabled = !refreshing)
        }
    }
}

internal fun partnerAlertTitle(type: String): String = when {
    type == "food_delivery_account_updated" -> "Delivery account updated"
    type == "food_delivery_document_reviewed" || type == "captain.review.updated" -> "Review decision updated"
    type == "captain.restricted" -> "Moped account needs attention"
    type == "captain.document.expiring" -> "Document needs renewal"
    type.endsWith("application.approved") -> "Application approved"
    type.endsWith("application.rejected") -> "Application needs attention"
    type.endsWith("document.reviewed") -> "Document review updated"
    type.endsWith("offer.new") || type == "captain.offer" || type == "food_delivery_offer" -> "New work request"
    else -> type.substringAfterLast('.').replace('_', ' ').replaceFirstChar { it.uppercase() }
}

@Composable
internal fun BusinessCard(summary: BusinessSummary, modifier: Modifier = Modifier, onOpen: () -> Unit) {
    val icon = when (summary.business) {
        PartnerBusiness.HOME -> UsIcons.Home
        PartnerBusiness.FOOD_DELIVERY -> UsIcons.Utensils
        PartnerBusiness.MOPED -> UsIcons.MapPin
        PartnerBusiness.COMMERCE_DELIVERY -> UsIcons.Package
    }
    UsSurfaceCard(modifier, contentPadding = PaddingValues(UsTheme.spacing.l), onClick = if (summary.canOpen) onOpen else null) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Surface(color = UsTheme.extended.accentSolid.copy(alpha = 0.08f), shape = RoundedCornerShape(UsTheme.radii.medium)) {
                Icon(icon, null, Modifier.padding(UsTheme.spacing.m).size(24.dp), tint = UsTheme.extended.accentSolid)
            }
            Spacer(Modifier.weight(1f))
            if (summary.canOpen) Icon(UsIcons.ChevronRight, null, Modifier.size(18.dp), tint = UsTheme.extended.textMuted)
        }
        Text(summary.business.title, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold)
        BusinessStatusBadge(summary.status)
    }
}

@Composable
private fun BusinessStatusBadge(status: BusinessStatus) {
    val color = when (status) {
        BusinessStatus.APPROVED -> UsTheme.extended.statusSuccess
        BusinessStatus.REVIEW -> UsTheme.extended.statusWarning
        BusinessStatus.ACTION, BusinessStatus.SUSPENDED -> UsTheme.extended.statusDanger
        else -> UsTheme.extended.textMuted
    }
    Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs), verticalAlignment = Alignment.CenterVertically) {
        Icon(if (status == BusinessStatus.APPROVED) UsIcons.Check else if (status == BusinessStatus.REVIEW) UsIcons.Clock else UsIcons.Info,
            null, Modifier.size(14.dp), tint = color)
        Text(status.label, style = MaterialTheme.typography.labelSmall, color = color)
    }
}

@Composable
private fun ActionCard(title: String, subtitle: String, icon: ImageVector, onClick: () -> Unit) {
    UsSurfaceCard(Modifier.fillMaxWidth(), onClick = onClick) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            Icon(icon, null, tint = UsTheme.extended.accentSolid)
            Column(Modifier.weight(1f)) { Text(title, style = MaterialTheme.typography.titleSmall); Text(subtitle, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textSecondary) }
            Icon(UsIcons.ChevronRight, null)
        }
    }
}

@Composable
private fun AccountMenuItem(title: String, icon: ImageVector, onClick: () -> Unit) {
    TextButton(onClick, Modifier.fillMaxWidth().heightIn(min = 48.dp)) {
        Icon(icon, null); Spacer(Modifier.width(UsTheme.spacing.l)); Text(title, Modifier.weight(1f), color = UsTheme.extended.textPrimary)
        Icon(UsIcons.ChevronRight, null)
    }
}
