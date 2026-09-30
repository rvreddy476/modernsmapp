package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Category
import com.us.android.core.commerce.model.CategoryNode
import com.us.android.core.commerce.model.listableCategories
import com.us.android.core.commerce.model.listableCategoriesFromFlat
import org.junit.Test

/**
 * Which categories a seller may list a product under (2026-09-30).
 *
 * From the tree: every active, listable node, labelled by its path, so a
 * heading that only groups others ("Books") is never offered and two
 * "Accessories" leaves under different headings stay distinguishable. From
 * the flat list (an older server): a node nobody hangs a child under.
 */
class ListableCategoriesTest {

    private fun node(
        id: String,
        name: String,
        listable: Boolean = true,
        active: Boolean = true,
        children: List<CategoryNode> = emptyList(),
    ) = CategoryNode(id, name, listable, active, children)

    @Test
    fun `a heading is not offered, its listable leaves are, by path`() {
        val tree = listOf(
            node(
                "books",
                "Books",
                listable = false,
                children = listOf(node("textbooks", "Textbooks"), node("comics", "Comics")),
            ),
            node("phones", "Phones", children = listOf(node("phone-cases", "Cases"))),
        )

        val choices = listableCategories(tree)

        assertThat(choices.map { it.id }).containsExactly("textbooks", "comics", "phones", "phone-cases").inOrder()
        assertThat(choices.map { it.label }).containsExactly(
            "Books › Textbooks",
            "Books › Comics",
            "Phones",
            "Phones › Cases",
        ).inOrder()
    }

    @Test
    fun `an inactive subtree is skipped whole`() {
        val tree = listOf(
            node("old", "Old", active = false, children = listOf(node("older", "Older"))),
            node("new", "New"),
        )
        assertThat(listableCategories(tree).map { it.id }).containsExactly("new")
    }

    @Test
    fun `a node without an id is skipped`() {
        assertThat(listableCategories(listOf(node("", "Ghost")))).isEmpty()
    }

    @Test
    fun `the flat list offers the nodes nobody hangs a child under, by path`() {
        val flat = listOf(
            category("books", "Books", parentId = null),
            category("textbooks", "Textbooks", parentId = "books"),
            category("phones", "Phones", parentId = null),
        )

        val choices = listableCategoriesFromFlat(flat)

        assertThat(choices.map { it.id }).containsExactly("textbooks", "phones").inOrder()
        assertThat(choices.first().label).isEqualTo("Books › Textbooks")
        assertThat(choices.last().label).isEqualTo("Phones")
    }

    private fun category(id: String, name: String, parentId: String?) = Category(
        id = id,
        name = name,
        slug = id,
        parentId = parentId,
        imageUrl = null,
        featured = false,
    )
}
