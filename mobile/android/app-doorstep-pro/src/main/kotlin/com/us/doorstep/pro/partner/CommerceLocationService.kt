package com.us.doorstep.pro.partner

import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.model.SessionState
import com.us.android.core.notifications.NotificationChannelSpec
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.serialization.json.*
import retrofit2.Retrofit
import java.time.Instant
import javax.inject.Inject
import javax.inject.Singleton

internal data class CommerceLocationState(val running:Boolean=false,val lastUpdated:Instant?=null,val error:String?=null)
@Singleton
internal class CommerceLocationStatus @Inject constructor() {
    private val mutable=MutableStateFlow(CommerceLocationState())
    val state=mutable.asStateFlow()
    fun started() {mutable.update {it.copy(running=true,error=null)}}
    fun updated() {mutable.update {it.copy(lastUpdated=Instant.now(),error=null)}}
    fun failed(message:String) {mutable.update {it.copy(error=message)}}
    fun stopped() {mutable.update {it.copy(running=false)}}
}
internal fun commerceLocationMessage(online:Boolean,state:CommerceLocationState,now:Instant):String? {
    if(!online) return null
    if(!state.running) return "Location sharing is paused on this phone. Resume it to keep receiving nearby requests. Availability expires when the server has no fresh location for 30 minutes."
    if(state.lastUpdated == null) return "Checking your current location before receiving nearby requests."
    if(java.time.Duration.between(state.lastUpdated,now).seconds >= 1800) return "Location is out of date. New requests are paused until a fresh location reaches the server. Your accepted delivery is unchanged."
    return state.error?.let { "$it New requests require a location update within 30 minutes. Your accepted delivery is unchanged." }
}

/** Same while-in-use foreground-service pattern as Food/Home; never requests background-location permission. */
@AndroidEntryPoint
class CommerceLocationService:Service() {
    @Inject lateinit var retrofit:Retrofit
    @Inject internal lateinit var status:CommerceLocationStatus
    @Inject lateinit var session:SessionStateProvider
    private val scope=CoroutineScope(SupervisorJob()+Dispatchers.Main.immediate)
    private var started=false
    override fun onBind(intent:Intent?):IBinder?=null
    override fun onStartCommand(intent:Intent?,flags:Int,startId:Int):Int {
        if(!started) startSharing()
        return START_NOT_STICKY
    }
    private fun startSharing() {
        try {ServiceCompat.startForeground(this,NOTIFICATION_ID,notification("Sharing your location while on duty"),ServiceInfo.FOREGROUND_SERVICE_TYPE_LOCATION)}
        catch(error:Exception){status.failed("Location sharing could not start. Reopen Commerce Delivery and resume it.");stopSelf();return}
        started=true;status.started()
        val api=retrofit.create(CommerceWorkApi::class.java)
        val owner=(session.sessionState.value as? SessionState.Authenticated)?.userId
        scope.launch { session.sessionState.collect {
            if(it != SessionState.Unknown && (it !is SessionState.Authenticated || (owner != null && it.userId != owner))) stopSelf()
        } }
        scope.launch {
            while(isActive) {
                try {
                    val work=parseWorkSession(commerceData(api.workSession()))
                    val availability=commerceData(api.availability()).commerceObject()
                    val online=(availability?.get("online") as? JsonPrimitive)?.booleanOrNull == true
                    if(work.category != PartnerBusiness.COMMERCE_DELIVERY || !online) {stopSelf();return@launch}
                    val point=currentLocation(this@CommerceLocationService)
                    commerceData(api.location(buildJsonObject {put("latitude",point.latitude);put("longitude",point.longitude)}))
                    status.updated();updateNotification("Sharing your location while on duty")
                } catch(cancel:CancellationException){throw cancel}
                catch(error:Exception){
                    status.failed("Couldn't update your delivery location. Check location permission and connection.")
                    updateNotification("Location update needed · open Commerce Delivery")
                }
                delay(60_000)
            }
        }
    }
    @Suppress("MissingPermission")
    private fun updateNotification(text:String) {runCatching {NotificationManagerCompat.from(this).notify(NOTIFICATION_ID,notification(text))}}
    private fun notification(text:String)=NotificationCompat.Builder(this,NotificationChannelSpec.COMMERCE_DELIVERY_DUTY.id)
        .setSmallIcon(android.R.drawable.ic_menu_mylocation).setContentTitle("Commerce Delivery · on duty")
        .setContentText(text).setOngoing(true).setOnlyAlertOnce(true).setCategory(NotificationCompat.CATEGORY_SERVICE)
        .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
        .apply {packageManager.getLaunchIntentForPackage(packageName)?.let {setContentIntent(PendingIntent.getActivity(this@CommerceLocationService,4202,it,PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))}}
        .build()
    override fun onDestroy(){status.stopped();scope.cancel();ServiceCompat.stopForeground(this,ServiceCompat.STOP_FOREGROUND_REMOVE);super.onDestroy()}
    companion object {
        private const val NOTIFICATION_ID=4202
        internal fun start(context:Context){ContextCompat.startForegroundService(context,Intent(context,CommerceLocationService::class.java))}
        internal fun stop(context:Context){context.stopService(Intent(context,CommerceLocationService::class.java))}
    }
}
