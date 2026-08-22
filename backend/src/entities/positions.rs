use sea_orm::entity::prelude::*;
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "positions")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub agent_id: Uuid,
    #[sea_orm(primary_key, auto_increment = false)]
    pub symbol: String,
    pub qty: i32,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {}

impl ActiveModelBehavior for ActiveModel {}
