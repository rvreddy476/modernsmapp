type FakeStoreProduct = {
  id: number
  title: string
  price: number
  description: string
  category: string
  image: string
  rating?: { rate?: number; count?: number }
}

const sourceURL = process.env.FAKESTORE_API_URL ?? "https://fakestoreapi.com/products"
const retailerName = process.env.RETAILER_NAME ?? "VChat Curated Retailer"
const usdToInr = Number(process.env.USD_TO_INR ?? "83")
const postgresContainer = process.env.POSTGRES_CONTAINER ?? "atpost_stack-postgres-1"

if (!Number.isFinite(usdToInr) || usdToInr <= 0) {
  throw new Error("USD_TO_INR must be a positive number")
}

const response = await fetch(sourceURL, { signal: AbortSignal.timeout(20_000) })
if (!response.ok) throw new Error(`Fake Store API returned HTTP ${response.status}`)

const products = (await response.json()) as FakeStoreProduct[]
if (!Array.isArray(products) || products.length === 0) throw new Error("Fake Store API returned no products")

function literal(value: string) {
  return `'${value.replaceAll("'", "''")}'`
}

function slug(value: string) {
  return value.toLowerCase().normalize("NFKD").replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "")
}

const statements: string[] = [
  "BEGIN;",
  `INSERT INTO sellers (user_id,seller_type,store_name,brand_name,legal_business_name,slug,description,email,verification_status,store_status,is_featured)
   VALUES ('00000000-0000-0000-0000-00000000f501','local_retailer',${literal(retailerName)},${literal(retailerName)},${literal(retailerName)},'fake-store-retailer','Imported demonstration catalog from Fake Store API','catalog@vchat.local','verified','active',TRUE)
   ON CONFLICT (user_id) DO UPDATE SET store_name=EXCLUDED.store_name,brand_name=EXCLUDED.brand_name,updated_at=NOW();`,
]

for (const product of products) {
  const categorySlug = `fakestore-${slug(product.category)}`
  const productSlug = `fakestore-${product.id}-${slug(product.title)}`
  const sku = `FAKESTORE-${String(product.id).padStart(3, "0")}`
  const sellingPrice = Math.round(product.price * usdToInr * 100) / 100
  const mrp = Math.ceil(sellingPrice * 1.2)
  const stock = Math.max(10, Math.min(250, product.rating?.count ?? 50))

  statements.push(
    `INSERT INTO product_categories (name,slug,description,is_active,is_featured)
     VALUES (${literal(product.category)},${literal(categorySlug)},${literal(`Products imported from Fake Store API: ${product.category}`)},TRUE,TRUE)
     ON CONFLICT (slug) DO UPDATE SET name=EXCLUDED.name,is_active=TRUE,updated_at=NOW();`,
    `INSERT INTO products (seller_id,category_id,title,short_title,slug,description,short_description,brand_name,manufacturer_name,product_type,condition,sku_root,status,visibility,approval_status,country_of_origin,return_policy_type,return_policy_days,avg_rating,review_count,is_featured,published_at)
     SELECT s.id,c.id,${literal(product.title)},${literal(product.title.slice(0, 100))},${literal(productSlug)},${literal(product.description)},${literal(product.description.slice(0, 180))},${literal(retailerName)},${literal(retailerName)},'physical','new',${literal(sku)},'active','public','approved','IN','7_days',7,${Number(product.rating?.rate ?? 0)},${Math.max(0, product.rating?.count ?? 0)},TRUE,NOW()
     FROM sellers s, product_categories c WHERE s.user_id='00000000-0000-0000-0000-00000000f501' AND c.slug=${literal(categorySlug)}
     ON CONFLICT (slug) DO UPDATE SET title=EXCLUDED.title,description=EXCLUDED.description,short_description=EXCLUDED.short_description,category_id=EXCLUDED.category_id,avg_rating=EXCLUDED.avg_rating,review_count=EXCLUDED.review_count,status='active',approval_status='approved',updated_at=NOW();`,
    `INSERT INTO product_variants (product_id,sku,mrp,selling_price,currency_code,status)
     SELECT id,${literal(sku)},${mrp},${sellingPrice},'INR','active' FROM products WHERE slug=${literal(productSlug)}
     ON CONFLICT (sku) DO UPDATE SET mrp=EXCLUDED.mrp,selling_price=EXCLUDED.selling_price,currency_code='INR',status='active',updated_at=NOW();`,
    `INSERT INTO inventory_items (variant_id,seller_id,total_qty,low_stock_alert)
     SELECT v.id,p.seller_id,${stock},5 FROM product_variants v JOIN products p ON p.id=v.product_id WHERE v.sku=${literal(sku)}
     ON CONFLICT (variant_id) DO UPDATE SET total_qty=EXCLUDED.total_qty,updated_at=NOW();`,
    `DELETE FROM product_attributes WHERE product_id=(SELECT id FROM products WHERE slug=${literal(productSlug)}) AND name IN ('source_image_url','source_product_id','source_name');`,
    `INSERT INTO product_attributes (product_id,name,value,sort_order)
     SELECT id,'source_image_url',${literal(product.image)},0 FROM products WHERE slug=${literal(productSlug)};`,
    `INSERT INTO product_attributes (product_id,name,value,sort_order)
     SELECT id,'source_product_id',${literal(String(product.id))},1 FROM products WHERE slug=${literal(productSlug)};`,
    `INSERT INTO product_attributes (product_id,name,value,sort_order)
     SELECT id,'source_name','Fake Store API',2 FROM products WHERE slug=${literal(productSlug)};`,
  )
}

statements.push(
  `UPDATE sellers SET total_products=(SELECT COUNT(*) FROM products WHERE seller_id=sellers.id),updated_at=NOW() WHERE user_id='00000000-0000-0000-0000-00000000f501';`,
  "COMMIT;",
)

const child = Bun.spawn([
  "docker", "exec", "-i", postgresContainer,
  "psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", "commerce_db",
], { stdin: new TextEncoder().encode(statements.join("\n")), stdout: "pipe", stderr: "pipe" })

const [exitCode, stdout, stderr] = await Promise.all([
  child.exited,
  new Response(child.stdout).text(),
  new Response(child.stderr).text(),
])

if (exitCode !== 0) throw new Error(`Catalog import failed:\n${stderr || stdout}`)
console.log(`Imported ${products.length} products for retailer "${retailerName}" at ₹${usdToInr}/USD.`)
