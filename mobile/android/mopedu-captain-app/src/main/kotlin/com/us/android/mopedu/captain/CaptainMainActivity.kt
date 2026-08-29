package com.us.android.mopedu.captain

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.rememberNavController
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.mopedu.captain.navigation.MopeduCaptainRoute
import com.us.android.feature.mopedu.captain.navigation.mopeduCaptainScreen
import dagger.hilt.android.AndroidEntryPoint

@AndroidEntryPoint
class CaptainMainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            UsTheme {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    color = MaterialTheme.colorScheme.background,
                ) {
                    val navController = rememberNavController()
                    NavHost(
                        navController = navController,
                        startDestination = MopeduCaptainRoute,
                    ) {
                        mopeduCaptainScreen(
                            onNavigateBack = { finish() },
                        )
                    }
                }
            }
        }
    }
}
