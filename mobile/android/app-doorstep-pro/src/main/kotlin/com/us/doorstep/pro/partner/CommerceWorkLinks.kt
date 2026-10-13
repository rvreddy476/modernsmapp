package com.us.doorstep.pro.partner

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

data class CommerceDestination(val page: String, val jobId: String? = null, val sequence: Long = 0)
@Singleton
class CommerceWorkLinks @Inject constructor() {
    private val mutable = MutableStateFlow<CommerceDestination?>(null)
    val destination = mutable.asStateFlow()
    private var sequence = 0L
    fun open(page: String, jobId: String? = null) {
        mutable.value = CommerceDestination(page, jobId?.takeIf { Regex("^[A-Za-z0-9-]{1,64}$").matches(it) }, ++sequence)
    }
    fun consumed() { mutable.value = null }
}
