use sea_orm::entity::prelude::*;
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "stocks")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub symbol: String,
    pub name: String,
    #[sea_orm(column_type = "Decimal(Some((20, 4)))")]
    pub fair: Decimal,
    #[sea_orm(column_type = "Decimal(Some((20, 4)))")]
    pub prev_close: Decimal,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {}

impl ActiveModelBehavior for ActiveModel {}
