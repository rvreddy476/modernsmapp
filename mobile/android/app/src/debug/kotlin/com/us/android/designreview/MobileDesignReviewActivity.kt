package com.us.android.designreview

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.auth.login.LoginScreen
import com.us.android.feature.auth.login.LoginUiState

/** Debug-only: inspect real composables with no network or authenticated content. */
class MobileDesignReviewActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            if (intent.getStringExtra("screen") == "login") {
                UsTheme {
                    LoginScreen(
                        state = LoginUiState(),
                        onIdentifierChange = {},
                        onPasswordChange = {},
                        onSubmit = {},
                    )
                }
            } else {
                MobileDesignReview()
            }
        }
    }
}
