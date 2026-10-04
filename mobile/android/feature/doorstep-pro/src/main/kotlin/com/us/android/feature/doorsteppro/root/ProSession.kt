package com.us.android.feature.doorsteppro.root

import com.us.android.feature.doorsteppro.data.ProfessionalDto
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The professional record the gate loaded, for the screens behind it (the
 * realtime topic is `doorstep.pro.<user_id>`). Nothing money-relevant; every
 * screen still reads its own data from the server.
 */
@Singleton
class ProSession @Inject constructor() {
    private val _professional = MutableStateFlow<ProfessionalDto?>(null)
    val professional: StateFlow<ProfessionalDto?> = _professional.asStateFlow()

    fun set(professional: ProfessionalDto?) {
        _professional.value = professional
    }
}
