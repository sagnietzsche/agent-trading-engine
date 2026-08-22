use sea_orm::entity::prelude::*;
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "orders")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i64,
    pub agent_id: Uuid,
    pub symbol: String,
    pub side: String,
    pub kind: String,
    #[sea_orm(column_type = "Decimal(Some((20, 4)))", nullable)]
    pub price: Option<Decimal>,
    pub qty: i32,
    pub filled: i32,
    pub status: String,
    pub created_at: DateTimeUtc,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {}

impl ActiveModelBehavior for ActiveModel {}
