package com.us.android.feature.kitchen.root

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.food.network.CreateKitchenRequest
import com.us.android.core.food.repository.KitchenRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.feature.kitchen.ui.userMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.util.UUID
import javax.inject.Inject

data class CreateKitchenState(val saving: Boolean = false, val created: Boolean = false, val error: String? = null)

@HiltViewModel
class CreateKitchenViewModel @Inject constructor(private val kitchen: KitchenRepository) : ViewModel() {
    private val mutable = MutableStateFlow(CreateKitchenState())
    val state = mutable.asStateFlow()
    fun save(name: String, address: String, city: String) {
        if (mutable.value.saving || mutable.value.created) return
        if (name.isBlank() || address.isBlank() || city.isBlank()) {
            mutable.value = CreateKitchenState(error = "Enter your kitchen name, address and town.")
            return
        }
        mutable.value = CreateKitchenState(saving = true)
        viewModelScope.launch {
            val slug = name.trim().lowercase().replace(Regex("[^a-z0-9]+"), "-").trim('-').take(60) + "-" + UUID.randomUUID().toString().take(8)
            when (val result = kitchen.createRestaurant(CreateKitchenRequest(name.trim(), slug, address.trim(), city.trim()))) {
                is FoodResult.Success -> mutable.value = CreateKitchenState(created = true)
                is FoodResult.Failure -> mutable.value = CreateKitchenState(error = result.error.userMessage())
            }
        }
    }
}

@Composable
fun CreateKitchenScreen(onCreated: () -> Unit, onRefresh: () -> Unit, viewModel: CreateKitchenViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var name by rememberSaveable { mutableStateOf("") }
    var address by rememberSaveable { mutableStateOf("") }
    var city by rememberSaveable { mutableStateOf("") }
    LaunchedEffect(state.created) { if (state.created) onCreated() }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
        Text("Start your food business", style = MaterialTheme.typography.headlineMedium)
        Text("Create your kitchen, then add your menu and verification details.")
        OutlinedTextField(name, { name = it.take(120) }, label = { Text("Kitchen or restaurant name") }, singleLine = true, enabled = !state.saving, modifier = Modifier.fillMaxWidth())
        OutlinedTextField(address, { address = it.take(240) }, label = { Text("Kitchen address") }, enabled = !state.saving, modifier = Modifier.fillMaxWidth())
        OutlinedTextField(city, { city = it.take(100) }, label = { Text("Town or city") }, singleLine = true, enabled = !state.saving, modifier = Modifier.fillMaxWidth())
        state.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        Button(onClick = { viewModel.save(name, address, city) }, enabled = !state.saving && !state.created, modifier = Modifier.fillMaxWidth().heightIn(min = 52.dp)) {
            Text(if (state.saving) "Creating…" else "Create kitchen")
        }
        TextButton(onClick = onRefresh, enabled = !state.saving) { Text("Already applied? Refresh status") }
    }
}
