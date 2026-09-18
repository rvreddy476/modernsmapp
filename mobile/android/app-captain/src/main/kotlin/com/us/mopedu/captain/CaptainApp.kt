package com.us.mopedu.captain

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.us.android.core.auth.AuthRepository
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.theme.MomentumWordmarkFontFamily
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.SessionState
import com.us.android.feature.auth.login.LoginRoute
import com.us.android.feature.auth.register.RegisterRoute
import com.us.android.feature.auth.verify.VerifyEmailRoute
import com.us.android.feature.mopedu.captain.location.CaptainDuty
import com.us.android.feature.mopedu.captain.location.OfflineReason
import com.us.android.feature.mopedu.captain.navigation.MopeduCaptainRoute
import com.us.android.feature.mopedu.captain.navigation.mopeduCaptainScreen
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable
import javax.inject.Inject

@Serializable
data object CaptainSignInRoute

@Serializable
data object CaptainRegisterRoute

@Serializable
data class CaptainVerifyEmailRoute(val verificationToken: String, val email: String)

/**
 * The shell, driven by the session: sign-in, registration and email
 * verification are :feature:auth's own screens; an authenticated session opens
 * the captain console, keyed by user so a sign-out and sign-in as someone else
 * never shows the previous captain for a frame.
 */
@Composable
fun CaptainApp(sessionStateProvider: SessionStateProvider) {
    val session by sessionStateProvider.sessionState.collectAsStateWithLifecycle()
    when (val current = session) {
        SessionState.Unknown -> CaptainSplash()
        is SessionState.Authenticated -> key(current.userId) {
            val shell = hiltViewModel<CaptainShellViewModel>(key = "captain-${current.userId}")
            val navController = rememberNavController()
            NavHost(navController = navController, startDestination = MopeduCaptainRoute) {
                mopeduCaptainScreen(onSignOut = shell::signOut)
            }
        }
        else -> CaptainSignIn()
    }
}

/** Offline first — while the session can still tell the server — then sign out. */
@HiltViewModel
class CaptainShellViewModel @Inject constructor(
    private val auth: AuthRepository,
    private val duty: CaptainDuty,
) : ViewModel() {
    fun signOut() {
        viewModelScope.launch {
            if (duty.isOnline) duty.requestStop(OfflineReason.SIGNED_OUT)
            auth.logout()
        }
    }
}

/** The brand mark while the session is restored. */
@Composable
private fun CaptainSplash() {
    UsScaffold { _ ->
        Column(
            modifier = Modifier.fillMaxSize(),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(text = "Mopedu", fontFamily = MomentumWordmarkFontFamily, fontSize = 44.sp, color = UsTheme.extended.textPrimary)
            Text(
                text = "CAPTAIN",
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.accentSolid,
            )
        }
    }
}

@Composable
private fun CaptainSignIn() {
    val navController = rememberNavController()
    val backToSignIn: () -> Unit = {
        navController.navigate(CaptainSignInRoute) {
            popUpTo<CaptainSignInRoute> { inclusive = true }
        }
    }
    NavHost(navController = navController, startDestination = CaptainSignInRoute) {
        composable<CaptainSignInRoute> {
            LoginRoute(
                onCreateAccount = { navController.navigate(CaptainRegisterRoute) },
                onNeedsVerification = { token, email -> navController.navigate(CaptainVerifyEmailRoute(token, email)) },
            )
        }
        composable<CaptainRegisterRoute> {
            RegisterRoute(
                onNeedsVerification = { token, email ->
                    navController.navigate(CaptainVerifyEmailRoute(token, email)) {
                        popUpTo<CaptainRegisterRoute> { inclusive = true }
                    }
                },
                onBackToLogin = { navController.popBackStack() },
            )
        }
        composable<CaptainVerifyEmailRoute> { entry ->
            val route = entry.toRoute<CaptainVerifyEmailRoute>()
            VerifyEmailRoute(
                verificationToken = route.verificationToken,
                email = route.email,
                onVerified = backToSignIn,
                onBackToLogin = backToSignIn,
            )
        }
    }
}
