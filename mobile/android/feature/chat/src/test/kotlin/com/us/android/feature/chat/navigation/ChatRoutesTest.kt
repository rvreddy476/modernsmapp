package com.us.android.feature.chat.navigation

import androidx.compose.material3.Text
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.google.common.truth.Truth.assertThat
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The 2026-09-05 routes — the one chat screen, add members, join by link,
 * invitations, requests, the community screens — resolve their serializers
 * and carry their arguments through `toRoute`. Same discipline as
 * [ChatNavigationTest]: the real route types and the real helpers, stub
 * destinations, so a renamed field fails here rather than as a blank screen.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ChatRoutesTest {

    @get:Rule
    val composeRule = createComposeRule()

    private lateinit var navController: NavHostController

    private val currentRoute: String?
        get() = navController.currentBackStackEntry?.destination?.route

    private fun setUpGraph() {
        composeRule.setContent {
            navController = rememberNavController()
            NavHost(navController = navController, startDestination = ChatHomeRoute) {
                composable<ChatHomeRoute> { Text("home") }
                composable<ChatRequestsListRoute> { Text("requests") }
                composable<InvitationsRoute> { Text("invitations") }
                composable<GroupAddMembersRoute> { entry ->
                    Text("add:${entry.toRoute<GroupAddMembersRoute>().conversationId}")
                }
                composable<JoinByLinkRoute> { entry -> Text("join:${entry.toRoute<JoinByLinkRoute>().code}") }
                composable<CommunityCreateRoute> { Text("create") }
                composable<CommunityEditRoute> { entry ->
                    Text(
                        "edit:${entry.toRoute<CommunityEditRoute>().communityId}"
                    )
                }
                composable<CommunityPageRoute> { entry ->
                    Text(
                        "page:${entry.toRoute<CommunityPageRoute>().communityId}"
                    )
                }
                composable<CommunityAdminsRoute> { entry ->
                    Text("admins:${entry.toRoute<CommunityAdminsRoute>().communityId}")
                }
                composable<CommunityPostRoute> { entry ->
                    Text(
                        "post:${entry.toRoute<CommunityPostRoute>().communityId}"
                    )
                }
            }
        }
    }

    private fun navigate(block: NavHostController.() -> Unit) {
        composeRule.runOnUiThread { navController.block() }
        composeRule.waitForIdle()
    }

    @Test
    fun `the chat home is the graph's start`() {
        setUpGraph()
        assertThat(currentRoute).contains(ChatHomeRoute::class.qualifiedName)
        composeRule.onNodeWithText("home").assertExists()
    }

    @Test
    fun `the header's doors open the requests and invitations lists`() {
        setUpGraph()
        navigate { navigateToChatRequestsList() }
        composeRule.onNodeWithText("requests").assertExists()
        navigate { navigateToInvitations() }
        composeRule.onNodeWithText("invitations").assertExists()
    }

    @Test
    fun `add members carries the conversation id`() {
        setUpGraph()
        navigate { navigateToGroupAddMembers("conv-7") }
        assertThat(
            navController.currentBackStackEntry!!.toRoute<GroupAddMembersRoute>().conversationId
        ).isEqualTo("conv-7")
        composeRule.onNodeWithText("add:conv-7").assertExists()
    }

    @Test
    fun `join by link carries the code, and none for the in-app entry`() {
        setUpGraph()
        navigate { navigateToJoinByLink("k7Qm") }
        composeRule.onNodeWithText("join:k7Qm").assertExists()
        navigate { navigateToJoinByLink() }
        assertThat(navController.currentBackStackEntry!!.toRoute<JoinByLinkRoute>().code).isEmpty()
    }

    @Test
    fun `the community screens carry the community id`() {
        setUpGraph()
        navigate { navigateToCommunityCreate() }
        composeRule.onNodeWithText("create").assertExists()
        navigate { navigateToCommunity("riders_1788614077") }
        composeRule.onNodeWithText("page:riders_1788614077").assertExists()
        navigate { navigateToCommunityEdit("riders_1788614077") }
        composeRule.onNodeWithText("edit:riders_1788614077").assertExists()
        navigate { navigateToCommunityAdmins("riders_1788614077") }
        composeRule.onNodeWithText("admins:riders_1788614077").assertExists()
        navigate { navigateToCommunityPost("riders_1788614077") }
        assertThat(navController.currentBackStackEntry!!.toRoute<CommunityPostRoute>().communityId)
            .isEqualTo("riders_1788614077")
    }

    @Test
    fun `back from a community page returns to the chat home`() {
        setUpGraph()
        navigate { navigateToCommunity("c1") }
        navigate { popBackStack() }
        assertThat(currentRoute).contains(ChatHomeRoute::class.qualifiedName)
    }
}
