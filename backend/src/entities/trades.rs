use sea_orm::entity::prelude::*;
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "trades")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub symbol: String,
    #[sea_orm(column_type = "Decimal(Some((20, 4)))")]
    pub price: Decimal,
    pub qty: i32,
    pub buyer: Uuid,
    pub seller: Uuid,
    pub taker_order: i64,
    #[sea_orm(column_type = "Decimal(Some((20, 4)))")]
    pub buyer_equity: Decimal,
    #[sea_orm(column_type = "Decimal(Some((20, 4)))")]
    pub seller_equity: Decimal,
    #[sea_orm(column_type = "Decimal(Some((10, 6)))")]
    pub gini_after: Decimal,
    pub ts: DateTimeUtc,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {}

impl ActiveModelBehavior for ActiveModel {}
