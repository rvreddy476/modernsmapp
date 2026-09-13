package com.us.android.feature.kitchen.menu

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.MenuCategoryDto
import com.us.android.core.food.network.MenuCategoryRequest
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.KitchenRepository
import com.us.android.feature.kitchen.navigation.restaurantId
import com.us.android.feature.kitchen.ui.asMessage
import com.us.android.feature.kitchen.ui.success
import com.us.android.feature.kitchen.ui.userMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class MenuUiState(
    val loading: Boolean = true,
    val categories: List<MenuCategoryDto> = emptyList(),
    val loadError: String? = null,
    val busyItemIds: Set<String> = emptySet(),
    /** Non-null while the "new category" dialog is open. */
    val categoryDraft: String? = null,
    val addingCategory: Boolean = false,
    val message: UsMessage? = null,
)

/** The menu with its stock switches (`PATCH …/menu/items/:id/availability`). */
@HiltViewModel
class MenuViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val kitchen: KitchenRepository,
) : ViewModel() {

    private val restaurantId = savedStateHandle.restaurantId()

    /**
     * Categories created here that the server does not list yet.
     *
     * ROUTE GAP: the partner category list is `GetMenu`, which INNER JOINs
     * menu items — a category with no dishes is never returned. Without this a
     * new category would vanish the moment it was created, before the partner
     * could add its first dish. Dropped once the server lists it.
     */
    private val createdCategories = LinkedHashMap<String, MenuCategoryDto>()

    private val _state = MutableStateFlow(MenuUiState())
    val state: StateFlow<MenuUiState> = _state.asStateFlow()

    fun refresh() {
        viewModelScope.launch {
            when (val result = kitchen.menuCategories(restaurantId)) {
                is FoodResult.Success -> {
                    val listed = result.value
                    createdCategories.keys.removeAll(listed.map { it.id }.toSet())
                    val merged = (listed + createdCategories.values).sortedBy { it.sortOrder }
                    _state.update { it.copy(loading = false, loadError = null, categories = merged) }
                }
                is FoodResult.Failure -> _state.update {
                    if (it.categories.isEmpty()) {
                        it.copy(loading = false, loadError = result.error.userMessage())
                    } else {
                        it.copy(loading = false, message = result.error.asMessage())
                    }
                }
            }
        }
    }

    /** Optimistic: the switch moves at once and moves back if the server refuses. */
    fun setAvailability(itemId: String, available: Boolean) {
        if (itemId in _state.value.busyItemIds) return
        _state.update {
            it.copy(busyItemIds = it.busyItemIds + itemId, categories = withAvailability(it.categories, itemId, available))
        }
        viewModelScope.launch {
            when (val result = kitchen.setMenuItemAvailability(itemId, available)) {
                is FoodResult.Success -> _state.update {
                    it.copy(
                        busyItemIds = it.busyItemIds - itemId,
                        message = success(if (available) "Back in stock" else "Marked out of stock"),
                    )
                }
                is FoodResult.Failure -> _state.update {
                    it.copy(
                        busyItemIds = it.busyItemIds - itemId,
                        categories = withAvailability(it.categories, itemId, !available),
                        message = result.error.asMessage(),
                    )
                }
            }
        }
    }

    fun openAddCategory() {
        _state.update { it.copy(categoryDraft = "") }
    }

    fun onCategoryDraft(value: String) {
        _state.update { it.copy(categoryDraft = value.take(MAX_CATEGORY_NAME)) }
    }

    fun closeAddCategory() {
        _state.update { it.copy(categoryDraft = null, addingCategory = false) }
    }

    fun addCategory() {
        val name = _state.value.categoryDraft?.trim().orEmpty()
        if (name.isEmpty() || _state.value.addingCategory) return
        _state.update { it.copy(addingCategory = true) }
        viewModelScope.launch {
            val sortOrder = (_state.value.categories.maxOfOrNull { it.sortOrder } ?: 0) + 1
            when (val result = kitchen.createMenuCategory(restaurantId, MenuCategoryRequest(name = name, sortOrder = sortOrder))) {
                is FoodResult.Success -> {
                    createdCategories[result.value.id] = result.value.copy(items = emptyList())
                    _state.update { it.copy(categoryDraft = null, addingCategory = false, message = success("Category added")) }
                    refresh()
                }
                is FoodResult.Failure -> _state.update { it.copy(addingCategory = false, message = result.error.asMessage()) }
            }
        }
    }

    fun deleteCategory(categoryId: String) {
        viewModelScope.launch {
            when (val result = kitchen.deleteMenuCategory(categoryId)) {
                is FoodResult.Success -> {
                    createdCategories.remove(categoryId)
                    _state.update { it.copy(message = success("Category removed")) }
                    refresh()
                }
                is FoodResult.Failure -> _state.update { it.copy(message = result.error.asMessage()) }
            }
        }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    private fun withAvailability(categories: List<MenuCategoryDto>, itemId: String, available: Boolean) =
        categories.map { category ->
            category.copy(items = category.items.map { if (it.id == itemId) it.copy(isAvailable = available) else it })
        }

    private companion object {
        const val MAX_CATEGORY_NAME = 80
    }
}
